// Package schema 实现零外部 protoc 依赖的纯 Go Protobuf (.proto) 词法/语法解析器、
// 运行时动态描述符注册中心 (protodesc + dynamicpb) 以及无 Schema 场景下的 protowire Tag 树降级解码。
package schema

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

var (
	ErrProtoSyntax = errors.New("schema: proto3 syntax parse error")
)

var scalarTypeMap = map[string]descriptorpb.FieldDescriptorProto_Type{
	"double":   descriptorpb.FieldDescriptorProto_TYPE_DOUBLE,
	"float":    descriptorpb.FieldDescriptorProto_TYPE_FLOAT,
	"int64":    descriptorpb.FieldDescriptorProto_TYPE_INT64,
	"uint64":   descriptorpb.FieldDescriptorProto_TYPE_UINT64,
	"int32":    descriptorpb.FieldDescriptorProto_TYPE_INT32,
	"fixed64":  descriptorpb.FieldDescriptorProto_TYPE_FIXED64,
	"fixed32":  descriptorpb.FieldDescriptorProto_TYPE_FIXED32,
	"bool":     descriptorpb.FieldDescriptorProto_TYPE_BOOL,
	"string":   descriptorpb.FieldDescriptorProto_TYPE_STRING,
	"bytes":    descriptorpb.FieldDescriptorProto_TYPE_BYTES,
	"uint32":   descriptorpb.FieldDescriptorProto_TYPE_UINT32,
	"sfixed32": descriptorpb.FieldDescriptorProto_TYPE_SFIXED32,
	"sfixed64": descriptorpb.FieldDescriptorProto_TYPE_SFIXED64,
	"sint32":   descriptorpb.FieldDescriptorProto_TYPE_SINT32,
	"sint64":   descriptorpb.FieldDescriptorProto_TYPE_SINT64,
}

// ParseProto3Source 将 .proto 文本源码纯内存解析为官方 descriptorpb.FileDescriptorProto，
// 无需宿主机安装 protoc 二进制工具或引入任何外部第三方编译依赖。
func ParseProto3Source(filename, src string) (*descriptorpb.FileDescriptorProto, error) {
	cleanName := filepath.Base(strings.TrimSpace(filename))
	if cleanName == "" || cleanName == "." || cleanName == "/" {
		cleanName = "uploaded.proto"
	}

	tokens, err := tokenizeProto(src)
	if err != nil {
		return nil, err
	}
	p := &protoParser{
		tokens: tokens,
		pos:    0,
	}
	fd, err := p.parseFile(cleanName)
	if err != nil {
		return nil, err
	}
	resolveFileTypes(fd)
	return fd, nil
}

type protoParser struct {
	tokens []string
	pos    int
}

func (p *protoParser) eof() bool {
	return p.pos >= len(p.tokens)
}

func (p *protoParser) peek() string {
	if p.eof() {
		return ""
	}
	return p.tokens[p.pos]
}

func (p *protoParser) next() string {
	if p.eof() {
		return ""
	}
	tok := p.tokens[p.pos]
	p.pos++
	return tok
}

func (p *protoParser) expect(expected string) error {
	got := p.next()
	if got != expected {
		return fmt.Errorf("%w: expected %q, got %q", ErrProtoSyntax, expected, got)
	}
	return nil
}

func (p *protoParser) parseFile(filename string) (*descriptorpb.FileDescriptorProto, error) {
	fd := &descriptorpb.FileDescriptorProto{
		Name:   proto.String(filename),
		Syntax: proto.String("proto3"),
	}

	for !p.eof() {
		tok := p.next()
		switch tok {
		case ";":
			continue
		case "syntax":
			if err := p.expect("="); err != nil {
				return nil, err
			}
			syn := trimQuotes(p.next())
			if syn != "" {
				fd.Syntax = proto.String(syn)
			}
			_ = p.consumeOptional(";")
		case "package":
			pkg, err := p.readQualifiedIdent()
			if err != nil {
				return nil, err
			}
			fd.Package = proto.String(pkg)
			_ = p.consumeOptional(";")
		case "option":
			if err := p.skipStatement(); err != nil {
				return nil, err
			}
		case "import":
			if p.peek() == "public" || p.peek() == "weak" {
				p.next()
			}
			dep := trimQuotes(p.next())
			if dep != "" {
				fd.Dependency = append(fd.Dependency, dep)
			}
			_ = p.consumeOptional(";")
		case "message":
			msg, err := p.parseMessage()
			if err != nil {
				return nil, err
			}
			fd.MessageType = append(fd.MessageType, msg)
		case "enum":
			en, err := p.parseEnum()
			if err != nil {
				return nil, err
			}
			fd.EnumType = append(fd.EnumType, en)
		default:
			return nil, fmt.Errorf("%w: unexpected top-level token %q", ErrProtoSyntax, tok)
		}
	}
	return fd, nil
}

func (p *protoParser) parseMessage() (*descriptorpb.DescriptorProto, error) {
	msgName := p.next()
	if msgName == "" {
		return nil, fmt.Errorf("%w: missing message name", ErrProtoSyntax)
	}
	if err := p.expect("{"); err != nil {
		return nil, err
	}

	msg := &descriptorpb.DescriptorProto{
		Name: proto.String(msgName),
	}

	for !p.eof() {
		if p.peek() == "}" {
			p.next()
			_ = p.consumeOptional(";")
			return msg, nil
		}
		tok := p.peek()
		switch tok {
		case ";":
			p.next()
		case "option", "reserved":
			p.next()
			if err := p.skipStatement(); err != nil {
				return nil, err
			}
		case "message":
			p.next()
			nestedMsg, err := p.parseMessage()
			if err != nil {
				return nil, err
			}
			msg.NestedType = append(msg.NestedType, nestedMsg)
		case "enum":
			p.next()
			nestedEnum, err := p.parseEnum()
			if err != nil {
				return nil, err
			}
			msg.EnumType = append(msg.EnumType, nestedEnum)
		case "map":
			p.next()
			field, entryMsg, err := p.parseMapField()
			if err != nil {
				return nil, err
			}
			msg.NestedType = append(msg.NestedType, entryMsg)
			msg.Field = append(msg.Field, field)
		default:
			field, err := p.parseNormalField()
			if err != nil {
				return nil, err
			}
			msg.Field = append(msg.Field, field)
		}
	}
	return nil, fmt.Errorf("%w: unclosed message %q", ErrProtoSyntax, msgName)
}

func (p *protoParser) parseMapField() (*descriptorpb.FieldDescriptorProto, *descriptorpb.DescriptorProto, error) {
	if err := p.expect("<"); err != nil {
		return nil, nil, err
	}
	keyTypeStr := p.next()
	if err := p.expect(","); err != nil {
		return nil, nil, err
	}
	valTypeStr, err := p.readQualifiedIdent()
	if err != nil {
		return nil, nil, err
	}
	if err := p.expect(">"); err != nil {
		return nil, nil, err
	}
	fieldName := p.next()
	if err := p.expect("="); err != nil {
		return nil, nil, err
	}
	numStr := p.next()
	fieldNum, err := strconv.Atoi(numStr)
	if err != nil || fieldNum <= 0 {
		return nil, nil, fmt.Errorf("%w: invalid map field number %q", ErrProtoSyntax, numStr)
	}
	if err := p.skipFieldOptionsAndSemicolon(); err != nil {
		return nil, nil, err
	}

	entryName := toPascalCase(fieldName) + "Entry"
	keyField := buildFieldDescriptor("key", 1, keyTypeStr, descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL)
	valField := buildFieldDescriptor("value", 2, valTypeStr, descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL)

	entryMsg := &descriptorpb.DescriptorProto{
		Name:  proto.String(entryName),
		Field: []*descriptorpb.FieldDescriptorProto{keyField, valField},
		Options: &descriptorpb.MessageOptions{
			MapEntry: proto.Bool(true),
		},
	}

	mapField := &descriptorpb.FieldDescriptorProto{
		Name:     proto.String(fieldName),
		Number:   proto.Int32(int32(fieldNum)),
		Label:    descriptorpb.FieldDescriptorProto_LABEL_REPEATED.Enum(),
		Type:     descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(),
		TypeName: proto.String(entryName),
		JsonName: proto.String(toLowerCamelCase(fieldName)),
	}
	return mapField, entryMsg, nil
}

func (p *protoParser) parseNormalField() (*descriptorpb.FieldDescriptorProto, error) {
	label := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL
	switch p.peek() {
	case "repeated":
		p.next()
		label = descriptorpb.FieldDescriptorProto_LABEL_REPEATED
	case "optional", "singular":
		p.next()
		label = descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL
	}

	typeName, err := p.readQualifiedIdent()
	if err != nil {
		return nil, err
	}
	fieldName := p.next()
	if fieldName == "" {
		return nil, fmt.Errorf("%w: missing field name after type %q", ErrProtoSyntax, typeName)
	}
	if err := p.expect("="); err != nil {
		return nil, err
	}
	numStr := p.next()
	fieldNum, err := strconv.Atoi(numStr)
	if err != nil || fieldNum <= 0 {
		return nil, fmt.Errorf("%w: invalid field number %q for %s", ErrProtoSyntax, numStr, fieldName)
	}
	if err := p.skipFieldOptionsAndSemicolon(); err != nil {
		return nil, err
	}

	return buildFieldDescriptor(fieldName, int32(fieldNum), typeName, label), nil
}

func buildFieldDescriptor(name string, number int32, typeStr string, label descriptorpb.FieldDescriptorProto_Label) *descriptorpb.FieldDescriptorProto {
	f := &descriptorpb.FieldDescriptorProto{
		Name:     proto.String(name),
		Number:   proto.Int32(number),
		Label:    label.Enum(),
		JsonName: proto.String(toLowerCamelCase(name)),
	}
	if st, ok := scalarTypeMap[typeStr]; ok {
		f.Type = st.Enum()
	} else {
		// 暂存为 TYPE_MESSAGE，后续由 resolveFileTypes 统一识别是 Enum 还是 Message 并补齐完整包名
		f.Type = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
		f.TypeName = proto.String(typeStr)
	}
	return f
}

func (p *protoParser) parseEnum() (*descriptorpb.EnumDescriptorProto, error) {
	enumName := p.next()
	if enumName == "" {
		return nil, fmt.Errorf("%w: missing enum name", ErrProtoSyntax)
	}
	if err := p.expect("{"); err != nil {
		return nil, err
	}
	en := &descriptorpb.EnumDescriptorProto{
		Name: proto.String(enumName),
	}
	for !p.eof() {
		if p.peek() == "}" {
			p.next()
			_ = p.consumeOptional(";")
			return en, nil
		}
		if p.peek() == ";" {
			p.next()
			continue
		}
		if p.peek() == "option" || p.peek() == "reserved" {
			p.next()
			if err := p.skipStatement(); err != nil {
				return nil, err
			}
			continue
		}
		valName := p.next()
		if err := p.expect("="); err != nil {
			return nil, err
		}
		numStr := p.next()
		valNum, err := strconv.Atoi(numStr)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid enum value %q for %s", ErrProtoSyntax, numStr, valName)
		}
		if err := p.skipFieldOptionsAndSemicolon(); err != nil {
			return nil, err
		}
		en.Value = append(en.Value, &descriptorpb.EnumValueDescriptorProto{
			Name:   proto.String(valName),
			Number: proto.Int32(int32(valNum)),
		})
	}
	return nil, fmt.Errorf("%w: unclosed enum %q", ErrProtoSyntax, enumName)
}

func (p *protoParser) readQualifiedIdent() (string, error) {
	var sb strings.Builder
	if p.peek() == "." {
		sb.WriteString(p.next())
	}
	first := p.next()
	if first == "" {
		return "", fmt.Errorf("%w: expected identifier", ErrProtoSyntax)
	}
	sb.WriteString(first)
	for p.peek() == "." {
		sb.WriteString(p.next())
		part := p.next()
		if part == "" {
			return "", fmt.Errorf("%w: trailing dot in identifier", ErrProtoSyntax)
		}
		sb.WriteString(part)
	}
	return sb.String(), nil
}

func (p *protoParser) skipFieldOptionsAndSemicolon() error {
	if p.peek() == "[" {
		p.next()
		depth := 1
		for !p.eof() && depth > 0 {
			t := p.next()
			if t == "[" {
				depth++
			} else if t == "]" {
				depth--
			}
		}
	}
	return p.expect(";")
}

func (p *protoParser) skipStatement() error {
	for !p.eof() {
		t := p.next()
		if t == ";" {
			return nil
		}
	}
	return fmt.Errorf("%w: unexpected EOF while skipping statement", ErrProtoSyntax)
}

func (p *protoParser) consumeOptional(tok string) bool {
	if p.peek() == tok {
		p.next()
		return true
	}
	return false
}

// resolveFileTypes 遍历 FileDescriptorProto 中所有自定义类型引用，区分 Enum 与 Message 并补齐限定名
func resolveFileTypes(fd *descriptorpb.FileDescriptorProto) {
	pkgPrefix := ""
	if fd.GetPackage() != "" {
		pkgPrefix = "." + fd.GetPackage()
	}

	knownEnums := make(map[string]bool)
	knownMsgs := make(map[string]bool)

	var collectMsg func(scope string, m *descriptorpb.DescriptorProto)
	collectMsg = func(scope string, m *descriptorpb.DescriptorProto) {
		full := scope + "." + m.GetName()
		knownMsgs[full] = true
		for _, en := range m.GetEnumType() {
			knownEnums[full+"."+en.GetName()] = true
		}
		for _, sub := range m.GetNestedType() {
			collectMsg(full, sub)
		}
	}

	for _, en := range fd.GetEnumType() {
		knownEnums[pkgPrefix+"."+en.GetName()] = true
	}
	for _, m := range fd.GetMessageType() {
		collectMsg(pkgPrefix, m)
	}

	var fixMsg func(scope string, m *descriptorpb.DescriptorProto)
	fixMsg = func(scope string, m *descriptorpb.DescriptorProto) {
		curFull := scope + "." + m.GetName()
		for _, f := range m.GetField() {
			rawType := f.GetTypeName()
			if rawType == "" {
				continue
			}
			candidates := []string{
				curFull + "." + rawType,
				pkgPrefix + "." + rawType,
				"." + strings.TrimPrefix(rawType, "."),
			}
			for _, cand := range candidates {
				if knownEnums[cand] {
					f.Type = descriptorpb.FieldDescriptorProto_TYPE_ENUM.Enum()
					f.TypeName = proto.String(cand)
					break
				}
				if knownMsgs[cand] {
					f.Type = descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum()
					f.TypeName = proto.String(cand)
					break
				}
			}
		}
		for _, sub := range m.GetNestedType() {
			fixMsg(curFull, sub)
		}
	}

	for _, m := range fd.GetMessageType() {
		fixMsg(pkgPrefix, m)
	}
}

func tokenizeProto(src string) ([]string, error) {
	var tokens []string
	n := len(src)
	i := 0
	for i < n {
		ch := src[i]
		if unicode.IsSpace(rune(ch)) {
			i++
			continue
		}
		// 单行注释 //
		if ch == '/' && i+1 < n && src[i+1] == '/' {
			i += 2
			for i < n && src[i] != '\n' {
				i++
			}
			continue
		}
		// 多行注释 /* ... */
		if ch == '/' && i+1 < n && src[i+1] == '*' {
			i += 2
			for i+1 < n && !(src[i] == '*' && src[i+1] == '/') {
				i++
			}
			i += 2
			continue
		}
		// 字符串字面量
		if ch == '"' || ch == '\'' {
			quote := ch
			start := i
			i++
			for i < n && src[i] != quote {
				if src[i] == '\\' && i+1 < n {
					i += 2
					continue
				}
				i++
			}
			if i >= n {
				return nil, fmt.Errorf("%w: unterminated string literal", ErrProtoSyntax)
			}
			i++
			tokens = append(tokens, src[start:i])
			continue
		}
		// 单字符分隔符
		switch ch {
		case '{', '}', ';', '=', '<', '>', ',', '[', ']', '(', ')', '.':
			tokens = append(tokens, string(ch))
			i++
			continue
		}
		// 标识符或数字（允许前导负号）
		start := i
		if ch == '-' {
			i++
		}
		for i < n {
			c := src[i]
			if unicode.IsLetter(rune(c)) || unicode.IsDigit(rune(c)) || c == '_' {
				i++
			} else {
				break
			}
		}
		if i == start {
			return nil, fmt.Errorf("%w: unexpected character %q", ErrProtoSyntax, ch)
		}
		tokens = append(tokens, src[start:i])
	}
	return tokens, nil
}

func trimQuotes(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && ((s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'')) {
		return s[1 : len(s)-1]
	}
	return s
}

func toLowerCamelCase(s string) string {
	parts := strings.Split(s, "_")
	if len(parts) <= 1 {
		return s
	}
	var sb strings.Builder
	sb.WriteString(parts[0])
	for _, p := range parts[1:] {
		if len(p) > 0 {
			sb.WriteString(strings.ToUpper(p[:1]) + p[1:])
		}
	}
	return sb.String()
}

func toPascalCase(s string) string {
	parts := strings.Split(s, "_")
	var sb strings.Builder
	for _, p := range parts {
		if len(p) > 0 {
			sb.WriteString(strings.ToUpper(p[:1]) + p[1:])
		}
	}
	return sb.String()
}
