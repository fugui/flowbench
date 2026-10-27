package schema

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

// TrafficDirection 定义报文流向（请求 vs 响应），用于支持同一 OpCode 下的双向消息路由
type TrafficDirection string

const (
	DirectionReq  TrafficDirection = "REQ"
	DirectionResp TrafficDirection = "RESP"
)

var (
	ErrMessageNotFound = errors.New("schema: protobuf message descriptor not found")
	ErrNoRouteBinding  = errors.New("schema: no opcode route binding found")
)

// OpCodeRouteBinding 定义 OpCode 与请求/响应 Protobuf Message 的动态路由契约
type OpCodeRouteBinding struct {
	OpCode          uint32 `json:"op_code"`
	OpCodeHex       string `json:"op_code_hex"`
	OpName          string `json:"op_name"`
	RequestMessage  string `json:"request_message,omitempty"`
	ResponseMessage string `json:"response_message,omitempty"`
}

// FieldSummary 描述单个 Protobuf 字段的元信息（供 Web 界面树状展示）
type FieldSummary struct {
	Number   int32  `json:"number"`
	Name     string `json:"name"`
	JSONName string `json:"json_name"`
	Type     string `json:"type"`
	Repeated bool   `json:"repeated"`
}

// MessageSchemaSummary 描述已注册的 Protobuf Message 结构摘要
type MessageSchemaSummary struct {
	FullName string         `json:"full_name"`
	Fields   []FieldSummary `json:"fields"`
}

// RegistryStatus 描述当前动态 Schema 注册表的运行态
type RegistryStatus struct {
	SchemaLoaded  bool                   `json:"schema_loaded"`
	FileName      string                 `json:"file_name"`
	PackageName   string                 `json:"package_name"`
	ProtoSource   string                 `json:"proto_source"`
	Messages      []MessageSchemaSummary `json:"messages"`
	Bindings      []OpCodeRouteBinding   `json:"bindings"`
	TotalMessages int                    `json:"total_messages"`
}

// Registry 维护运行时动态 Protobuf 描述符池与 (OpCode, Direction) 路由映射表
type Registry struct {
	mu          sync.RWMutex
	fileName    string
	packageName string
	protoSource string
	files       *protoregistry.Files
	msgMap      map[string]protoreflect.MessageDescriptor
	bindings    map[uint32]OpCodeRouteBinding
}

// NewDefaultSandboxRegistry 创建预加载了内置 flowbench.v1 协议 Schema 的动态注册中心
func NewDefaultSandboxRegistry() *Registry {
	reg := &Registry{
		msgMap:   make(map[string]protoreflect.MessageDescriptor),
		bindings: make(map[uint32]OpCodeRouteBinding),
	}
	_ = reg.LoadProtoSource("messages.proto", DefaultSandboxProtoSource, DefaultSandboxBindings())
	return reg
}

// NewEmptyRegistry 创建未导入任何 .proto 的空注册中心（用于验证无 Schema 降级与后续动态补传）
func NewEmptyRegistry() *Registry {
	return &Registry{
		msgMap:   make(map[string]protoreflect.MessageDescriptor),
		bindings: make(map[uint32]OpCodeRouteBinding),
	}
}

// ClearSchema 清空当前加载的描述符池，切换至无 Schema (Schema-Less) Tag 解析降级模式
func (r *Registry) ClearSchema() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fileName = ""
	r.packageName = ""
	r.protoSource = ""
	r.files = nil
	r.msgMap = make(map[string]protoreflect.MessageDescriptor)
}

// LoadProtoSource 纯 Go 在线解析 .proto 源码并热更新运行时描述符池与路由表
func (r *Registry) LoadProtoSource(filename, src string, bindings []OpCodeRouteBinding) error {
	fdProto, err := ParseProto3Source(filename, src)
	if err != nil {
		return err
	}
	fdSet := &descriptorpb.FileDescriptorSet{
		File: []*descriptorpb.FileDescriptorProto{fdProto},
	}
	return r.loadDescriptorSet(filename, src, fdSet, bindings)
}

// LoadFileDescriptorSetBytes 导入预编译的 FileDescriptorSet 二进制字节流 (*.desc / *.pb)
func (r *Registry) LoadFileDescriptorSetBytes(filename string, raw []byte, bindings []OpCodeRouteBinding) error {
	var fdSet descriptorpb.FileDescriptorSet
	if err := proto.Unmarshal(raw, &fdSet); err != nil {
		return fmt.Errorf("unmarshal FileDescriptorSet failed: %w", err)
	}
	return r.loadDescriptorSet(filename, "", &fdSet, bindings)
}

func (r *Registry) loadDescriptorSet(filename, rawSrc string, fdSet *descriptorpb.FileDescriptorSet, bindings []OpCodeRouteBinding) error {
	files, err := protodesc.NewFiles(fdSet)
	if err != nil {
		return fmt.Errorf("protodesc build files failed: %w", err)
	}

	newMsgMap := make(map[string]protoreflect.MessageDescriptor)
	var pkgName string

	var collectMessages func(mds protoreflect.MessageDescriptors)
	collectMessages = func(mds protoreflect.MessageDescriptors) {
		for i := 0; i < mds.Len(); i++ {
			md := mds.Get(i)
			if md.IsMapEntry() {
				continue
			}
			newMsgMap[string(md.FullName())] = md
			collectMessages(md.Messages())
		}
	}

	files.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if pkgName == "" {
			pkgName = string(fd.Package())
		}
		collectMessages(fd.Messages())
		return true
	})

	r.mu.Lock()
	defer r.mu.Unlock()

	r.fileName = filename
	r.packageName = pkgName
	r.protoSource = rawSrc
	r.files = files
	r.msgMap = newMsgMap

	if len(bindings) > 0 {
		r.bindings = make(map[uint32]OpCodeRouteBinding, len(bindings))
		for _, b := range bindings {
			if b.OpCodeHex == "" {
				b.OpCodeHex = fmt.Sprintf("0x%02X", b.OpCode)
			}
			r.bindings[b.OpCode] = b
		}
	}
	return nil
}

// UpdateBindings 动态更新 (OpCode, Direction) 与 Protobuf Message 的路由绑定关系
func (r *Registry) UpdateBindings(bindings []OpCodeRouteBinding) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bindings = make(map[uint32]OpCodeRouteBinding, len(bindings))
	for _, b := range bindings {
		if b.OpCodeHex == "" {
			b.OpCodeHex = fmt.Sprintf("0x%02X", b.OpCode)
		}
		r.bindings[b.OpCode] = b
	}
}

// ResolveDescriptor 根据 (OpCode, Direction) 查找对应的 protoreflect.MessageDescriptor
func (r *Registry) ResolveDescriptor(opCode uint32, dir TrafficDirection) (protoreflect.MessageDescriptor, string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	b, ok := r.bindings[opCode]
	if !ok {
		return nil, "", false
	}

	targetMsg := b.RequestMessage
	if dir == DirectionResp && b.ResponseMessage != "" {
		targetMsg = b.ResponseMessage
	} else if targetMsg == "" {
		targetMsg = b.ResponseMessage
	}
	if targetMsg == "" {
		return nil, b.OpName, false
	}

	md, found := r.lookupDescriptorLocked(targetMsg)
	return md, b.OpName, found
}

func (r *Registry) lookupDescriptorLocked(name string) (protoreflect.MessageDescriptor, bool) {
	trimmed := strings.TrimPrefix(strings.TrimSpace(name), ".")
	if md, ok := r.msgMap[trimmed]; ok {
		return md, true
	}
	if r.packageName != "" {
		withPkg := r.packageName + "." + trimmed
		if md, ok := r.msgMap[withPkg]; ok {
			return md, true
		}
	}
	return nil, false
}

// DecodePayload 使用 dynamicpb 将二进制 Protobuf 载荷反序列化为业务明文 JSON Map。
// 若当前尚未导入 .proto 或未命中 Message 映射，则自动优雅降级为 protowire Tag 树视图。
func (r *Registry) DecodePayload(opCode uint32, dir TrafficDirection, payload []byte) (decoded map[string]any, opName string, schemaDecoded bool) {
	if len(payload) == 0 {
		_, name, _ := r.ResolveDescriptor(opCode, dir)
		if name == "" {
			name = fmt.Sprintf("OpUnknown(0x%02X)", opCode)
		}
		return map[string]any{}, name, true
	}

	md, resolvedName, found := r.ResolveDescriptor(opCode, dir)
	if resolvedName == "" {
		resolvedName = fmt.Sprintf("OpCode(0x%02X)", opCode)
	}

	if found && md != nil {
		dynMsg := dynamicpb.NewMessage(md)
		if err := proto.Unmarshal(payload, dynMsg); err == nil {
			jsonBytes, mErr := (protojson.MarshalOptions{
				EmitUnpopulated: true,
			}).Marshal(dynMsg)
			if mErr == nil {
				var out map[string]any
				if err := json.Unmarshal(jsonBytes, &out); err == nil {
					return out, resolvedName, true
				}
			}
		}
	}

	// 优雅降级：无 Schema 时使用 protowire 解析原始 Tag 编号与 WireType 树
	fallback := DecodeWireTagsFallback(payload)
	return fallback, resolvedName, false
}

// EncodeJSONToPayload 将用户在 Web 界面编辑的 JSON 对象通过 dynamicpb 动态编码为确定性 Protobuf 二进制字节流
func (r *Registry) EncodeJSONToPayload(opCode uint32, dir TrafficDirection, jsonObj map[string]any) ([]byte, error) {
	md, _, found := r.ResolveDescriptor(opCode, dir)
	if !found || md == nil {
		return nil, fmt.Errorf("%w for opcode 0x%02X (%s)", ErrMessageNotFound, opCode, dir)
	}

	rawJSON, err := json.Marshal(jsonObj)
	if err != nil {
		return nil, err
	}

	dynMsg := dynamicpb.NewMessage(md)
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(rawJSON, dynMsg); err != nil {
		return nil, fmt.Errorf("protojson unmarshal into %s failed: %w", md.FullName(), err)
	}

	wireBytes, err := (proto.MarshalOptions{Deterministic: true}).Marshal(dynMsg)
	if err != nil {
		return nil, fmt.Errorf("deterministic proto marshal failed: %w", err)
	}
	return wireBytes, nil
}

// DecodeWireTagsFallback 在未导入 .proto 时，基于 protowire 提取二进制流中的 Tag 编号与原始数值
func DecodeWireTagsFallback(payload []byte) map[string]any {
	fields := parseWireMessageTags(payload, 0)
	return map[string]any{
		"_schema_less": true,
		"_hint":        "当前未绑定 .proto Schema，已自动降级为 Wire Tag 视图；导入 .proto 后将自动激活为业务字段名",
		"tags":         fields,
	}
}

func parseWireMessageTags(b []byte, depth int) map[string]any {
	res := make(map[string]any)
	rest := b
	for len(rest) > 0 {
		num, wtype, tagLen := protowire.ConsumeTag(rest)
		if tagLen < 0 {
			res["_raw_hex"] = strings.ToUpper(hex.EncodeToString(b))
			return res
		}
		rest = rest[tagLen:]
		key := fmt.Sprintf("tag_%d", num)

		var val any
		var valLen int
		switch wtype {
		case protowire.VarintType:
			v, n := protowire.ConsumeVarint(rest)
			valLen = n
			val = v
		case protowire.Fixed32Type:
			v, n := protowire.ConsumeFixed32(rest)
			valLen = n
			val = v
		case protowire.Fixed64Type:
			v, n := protowire.ConsumeFixed64(rest)
			valLen = n
			val = v
		case protowire.BytesType:
			v, n := protowire.ConsumeBytes(rest)
			valLen = n
			if n >= 0 {
				if utf8.Valid(v) && isPrintableASCIIOrUTF8(v) {
					val = string(v)
				} else if depth < 2 && len(v) > 0 && looksLikeNestedWire(v) {
					val = parseWireMessageTags(v, depth+1)
				} else {
					val = "0x" + strings.ToUpper(hex.EncodeToString(v))
				}
			}
		default:
			valLen = -1
		}

		if valLen < 0 {
			res["_raw_hex"] = strings.ToUpper(hex.EncodeToString(b))
			return res
		}
		rest = rest[valLen:]

		if existing, exists := res[key]; exists {
			switch s := existing.(type) {
			case []any:
				res[key] = append(s, val)
			default:
				res[key] = []any{s, val}
			}
		} else {
			res[key] = val
		}
	}
	return res
}

func isPrintableASCIIOrUTF8(b []byte) bool {
	if len(b) == 0 {
		return true
	}
	for _, r := range string(b) {
		if r < 0x20 && r != '\n' && r != '\r' && r != '\t' {
			return false
		}
	}
	return true
}

func looksLikeNestedWire(b []byte) bool {
	rest := b
	count := 0
	for len(rest) > 0 {
		num, wtype, n := protowire.ConsumeTag(rest)
		if n < 0 || num <= 0 || num > 10000 {
			return false
		}
		rest = rest[n:]
		vLen := protowire.ConsumeFieldValue(num, wtype, rest)
		if vLen < 0 {
			return false
		}
		rest = rest[vLen:]
		count++
	}
	return count > 0
}

// Status 返回当前 Schema 注册表的完整状态快照
func (r *Registry) Status() RegistryStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()

	msgs := make([]MessageSchemaSummary, 0, len(r.msgMap))
	for _, md := range r.msgMap {
		fields := make([]FieldSummary, 0, md.Fields().Len())
		for i := 0; i < md.Fields().Len(); i++ {
			fd := md.Fields().Get(i)
			fields = append(fields, FieldSummary{
				Number:   int32(fd.Number()),
				Name:     string(fd.Name()),
				JSONName: fd.JSONName(),
				Type:     fd.Kind().String(),
				Repeated: fd.IsList() || fd.IsMap(),
			})
		}
		msgs = append(msgs, MessageSchemaSummary{
			FullName: string(md.FullName()),
			Fields:   fields,
		})
	}
	sort.Slice(msgs, func(i, j int) bool {
		return msgs[i].FullName < msgs[j].FullName
	})

	bList := make([]OpCodeRouteBinding, 0, len(r.bindings))
	for _, b := range r.bindings {
		bList = append(bList, b)
	}
	sort.Slice(bList, func(i, j int) bool {
		return bList[i].OpCode < bList[j].OpCode
	})

	return RegistryStatus{
		SchemaLoaded:  len(r.msgMap) > 0,
		FileName:      r.fileName,
		PackageName:   r.packageName,
		ProtoSource:   r.protoSource,
		Messages:      msgs,
		Bindings:      bList,
		TotalMessages: len(msgs),
	}
}

// DefaultSandboxBindings 返回内置 flowbench-v1 沙箱的默认 OpCode 映射表
func DefaultSandboxBindings() []OpCodeRouteBinding {
	return []OpCodeRouteBinding{
		{OpCode: 0x01, OpCodeHex: "0x01", OpName: "OpPingReq(0x01)", RequestMessage: "flowbench.v1.PingRequest", ResponseMessage: "flowbench.v1.PongResponse"},
		{OpCode: 0x02, OpCodeHex: "0x02", OpName: "OpPongResp(0x02)", RequestMessage: "flowbench.v1.PongResponse", ResponseMessage: "flowbench.v1.PongResponse"},
		{OpCode: 0x11, OpCodeHex: "0x11", OpName: "OpOrderQuoteReq(0x11)", RequestMessage: "flowbench.v1.OrderQuoteRequest", ResponseMessage: "flowbench.v1.OrderQuoteResponse"},
		{OpCode: 0x12, OpCodeHex: "0x12", OpName: "OpOrderQuoteResp(0x12)", RequestMessage: "flowbench.v1.OrderQuoteResponse", ResponseMessage: "flowbench.v1.OrderQuoteResponse"},
		{OpCode: 0x21, OpCodeHex: "0x21", OpName: "OpCreditEvalReq(0x21)", RequestMessage: "flowbench.v1.CreditEvalRequest", ResponseMessage: "flowbench.v1.CreditEvalResponse"},
		{OpCode: 0x22, OpCodeHex: "0x22", OpName: "OpCreditEvalResp(0x22)", RequestMessage: "flowbench.v1.CreditEvalResponse", ResponseMessage: "flowbench.v1.CreditEvalResponse"},
		{OpCode: 0x31, OpCodeHex: "0x31", OpName: "OpBackendOrderQueryReq(0x31)", RequestMessage: "flowbench.v1.BackendOrderQueryRequest", ResponseMessage: "flowbench.v1.BackendOrderQueryResponse"},
		{OpCode: 0x32, OpCodeHex: "0x32", OpName: "OpBackendOrderQueryResp(0x32)", RequestMessage: "flowbench.v1.BackendOrderQueryResponse", ResponseMessage: "flowbench.v1.BackendOrderQueryResponse"},
		{OpCode: 0x41, OpCodeHex: "0x41", OpName: "OpBackendCreditQueryReq(0x41)", RequestMessage: "flowbench.v1.BackendCreditQueryRequest", ResponseMessage: "flowbench.v1.BackendCreditQueryResponse"},
		{OpCode: 0x42, OpCodeHex: "0x42", OpName: "OpBackendCreditQueryResp(0x42)", RequestMessage: "flowbench.v1.BackendCreditQueryResponse", ResponseMessage: "flowbench.v1.BackendCreditQueryResponse"},
		{OpCode: 0xFF, OpCodeHex: "0xFF", OpName: "OpErrorResp(0xFF)", RequestMessage: "flowbench.v1.ErrorResponse", ResponseMessage: "flowbench.v1.ErrorResponse"},
	}
}

// DefaultSandboxProtoSource 为内置参考沙箱默认加载的 .proto 源码
const DefaultSandboxProtoSource = `syntax = "proto3";

package flowbench.v1;

enum BizCode {
  BIZ_CODE_UNSPECIFIED   = 0;
  BIZ_OK                 = 200;
  BIZ_INVALID_ARGUMENT   = 400;
  BIZ_RISK_REJECTED      = 403;
  BIZ_NOT_FOUND          = 404;
  BIZ_OUT_OF_STOCK       = 409;
  BIZ_INTERNAL_ERROR     = 500;
  BIZ_DOWNSTREAM_TIMEOUT = 504;
}

message PingRequest {
  string client_id    = 1;
  string message      = 2;
  int64  sent_at_unix = 3;
}

message PongResponse {
  int32  biz_code        = 1;
  string server_id       = 2;
  string echo_message    = 3;
  string status          = 4;
  int64  replied_at_unix = 5;
}

message OrderQuoteRequest {
  string user_id          = 1;
  string sku_id           = 2;
  int32  quantity         = 3;
  int32  vip_level        = 4;
  string region           = 5;
  int64  client_timestamp = 6;
}

message OrderQuoteResponse {
  int32  biz_code              = 1;
  string biz_message           = 2;
  string sku_name              = 3;
  int64  unit_price_cents      = 4;
  int64  subtotal_cents        = 5;
  int64  discount_cents        = 6;
  int64  discounted_cents      = 7;
  int64  tax_cents             = 8;
  int64  shipping_cents        = 9;
  int64  final_amount_cents    = 10;
  string risk_decision         = 11;
  string fulfillment_warehouse = 12;
  string processing_digest     = 13;
  string trace_tag             = 14;
  int64  processed_at_unix     = 15;
}

message CreditEvalRequest {
  string user_id               = 1;
  int64  requested_limit_cents = 2;
  int64  annual_income_cents   = 3;
  int64  existing_debt_cents   = 4;
  int64  client_timestamp      = 5;
}

message CreditEvalResponse {
  int32  biz_code             = 1;
  string biz_message          = 2;
  string credit_grade         = 3;
  int32  dti_bps              = 4;
  int64  max_allowable_cents  = 5;
  int64  approved_limit_cents = 6;
  string decision             = 7;
  string processing_digest    = 8;
  string trace_tag            = 9;
  int64  processed_at_unix    = 10;
}

message BackendOrderQueryRequest {
  string sku_id  = 1;
  string user_id = 2;
  string region  = 3;
}

message BackendOrderQueryResponse {
  bool   found             = 1;
  string sku_id            = 2;
  string sku_name          = 3;
  int64  base_price_cents  = 4;
  int32  available_stock   = 5;
  int32  unit_weight_grams = 6;
  string warehouse_code    = 7;
  int32  user_credit_score = 8;
  bool   risk_blacklisted  = 9;
}

message BackendCreditQueryRequest {
  string user_id = 1;
}

message BackendCreditQueryResponse {
  bool   found                    = 1;
  string user_id                  = 2;
  int32  credit_score             = 3;
  int32  historical_default_count = 4;
  int32  account_age_months       = 5;
  bool   risk_blacklisted         = 6;
}

message ErrorResponse {
  int32  error_code    = 1;
  string error_message = 2;
  uint64 request_id    = 3;
}
`
