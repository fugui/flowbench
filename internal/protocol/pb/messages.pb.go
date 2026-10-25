// Package pb 包含 flowbench/v1/messages.proto 对应的 Protobuf 消息结构定义与反射注册。
// 严格仅使用 google.golang.org/protobuf 进行二进制封送，不引入任何 gRPC 依赖。
package pb

import (
	reflect "reflect"
	sync "sync"

	proto "google.golang.org/protobuf/proto"
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	descriptorpb "google.golang.org/protobuf/types/descriptorpb"
)

// BizCode 业务状态码枚举常量
type BizCode int32

const (
	BizCode_BIZ_CODE_UNSPECIFIED   BizCode = 0
	BizCode_BIZ_OK                 BizCode = 200
	BizCode_BIZ_INVALID_ARGUMENT   BizCode = 400
	BizCode_BIZ_RISK_REJECTED      BizCode = 403
	BizCode_BIZ_NOT_FOUND          BizCode = 404
	BizCode_BIZ_OUT_OF_STOCK       BizCode = 409
	BizCode_BIZ_INTERNAL_ERROR     BizCode = 500
	BizCode_BIZ_DOWNSTREAM_TIMEOUT BizCode = 504
)

// 0. PingRequest (OpCode: 0x01)
type PingRequest struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	ClientId      string                 `protobuf:"bytes,1,opt,name=client_id,json=clientId,proto3" json:"client_id,omitempty"`
	Message       string                 `protobuf:"bytes,2,opt,name=message,proto3" json:"message,omitempty"`
	SentAtUnix    int64                  `protobuf:"varint,3,opt,name=sent_at_unix,json=sentAtUnix,proto3" json:"sent_at_unix,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *PingRequest) Reset()         { resetMsg(x, 0) }
func (x *PingRequest) String() string { return protoimpl.X.MessageStringOf(x) }
func (*PingRequest) ProtoMessage()    {}
func (x *PingRequest) ProtoReflect() protoreflect.Message {
	return reflectMsg(x, 0)
}

// 1. PongResponse (OpCode: 0x02)
type PongResponse struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	BizCode       int32                  `protobuf:"varint,1,opt,name=biz_code,json=bizCode,proto3" json:"biz_code,omitempty"`
	ServerId      string                 `protobuf:"bytes,2,opt,name=server_id,json=serverId,proto3" json:"server_id,omitempty"`
	EchoMessage   string                 `protobuf:"bytes,3,opt,name=echo_message,json=echoMessage,proto3" json:"echo_message,omitempty"`
	Status        string                 `protobuf:"bytes,4,opt,name=status,proto3" json:"status,omitempty"`
	RepliedAtUnix int64                  `protobuf:"varint,5,opt,name=replied_at_unix,json=repliedAtUnix,proto3" json:"replied_at_unix,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *PongResponse) Reset()         { resetMsg(x, 1) }
func (x *PongResponse) String() string { return protoimpl.X.MessageStringOf(x) }
func (*PongResponse) ProtoMessage()    {}
func (x *PongResponse) ProtoReflect() protoreflect.Message {
	return reflectMsg(x, 1)
}

// 2. OrderQuoteRequest (OpCode: 0x11)
type OrderQuoteRequest struct {
	state           protoimpl.MessageState `protogen:"open.v1"`
	UserId          string                 `protobuf:"bytes,1,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`
	SkuId           string                 `protobuf:"bytes,2,opt,name=sku_id,json=skuId,proto3" json:"sku_id,omitempty"`
	Quantity        int32                  `protobuf:"varint,3,opt,name=quantity,proto3" json:"quantity,omitempty"`
	VipLevel        int32                  `protobuf:"varint,4,opt,name=vip_level,json=vipLevel,proto3" json:"vip_level,omitempty"`
	Region          string                 `protobuf:"bytes,5,opt,name=region,proto3" json:"region,omitempty"`
	ClientTimestamp int64                  `protobuf:"varint,6,opt,name=client_timestamp,json=clientTimestamp,proto3" json:"client_timestamp,omitempty"`
	unknownFields   protoimpl.UnknownFields
	sizeCache       protoimpl.SizeCache
}

func (x *OrderQuoteRequest) Reset()         { resetMsg(x, 2) }
func (x *OrderQuoteRequest) String() string { return protoimpl.X.MessageStringOf(x) }
func (*OrderQuoteRequest) ProtoMessage()    {}
func (x *OrderQuoteRequest) ProtoReflect() protoreflect.Message {
	return reflectMsg(x, 2)
}

// 3. OrderQuoteResponse (OpCode: 0x12)
type OrderQuoteResponse struct {
	state                protoimpl.MessageState `protogen:"open.v1"`
	BizCode              int32                  `protobuf:"varint,1,opt,name=biz_code,json=bizCode,proto3" json:"biz_code,omitempty"`
	BizMessage           string                 `protobuf:"bytes,2,opt,name=biz_message,json=bizMessage,proto3" json:"biz_message,omitempty"`
	SkuName              string                 `protobuf:"bytes,3,opt,name=sku_name,json=skuName,proto3" json:"sku_name,omitempty"`
	UnitPriceCents       int64                  `protobuf:"varint,4,opt,name=unit_price_cents,json=unitPriceCents,proto3" json:"unit_price_cents,omitempty"`
	SubtotalCents        int64                  `protobuf:"varint,5,opt,name=subtotal_cents,json=subtotalCents,proto3" json:"subtotal_cents,omitempty"`
	DiscountCents        int64                  `protobuf:"varint,6,opt,name=discount_cents,json=discountCents,proto3" json:"discount_cents,omitempty"`
	DiscountedCents      int64                  `protobuf:"varint,7,opt,name=discounted_cents,json=discountedCents,proto3" json:"discounted_cents,omitempty"`
	TaxCents             int64                  `protobuf:"varint,8,opt,name=tax_cents,json=taxCents,proto3" json:"tax_cents,omitempty"`
	ShippingCents        int64                  `protobuf:"varint,9,opt,name=shipping_cents,json=shippingCents,proto3" json:"shipping_cents,omitempty"`
	FinalAmountCents     int64                  `protobuf:"varint,10,opt,name=final_amount_cents,json=finalAmountCents,proto3" json:"final_amount_cents,omitempty"`
	RiskDecision         string                 `protobuf:"bytes,11,opt,name=risk_decision,json=riskDecision,proto3" json:"risk_decision,omitempty"`
	FulfillmentWarehouse string                 `protobuf:"bytes,12,opt,name=fulfillment_warehouse,json=fulfillmentWarehouse,proto3" json:"fulfillment_warehouse,omitempty"`
	ProcessingDigest     string                 `protobuf:"bytes,13,opt,name=processing_digest,json=processingDigest,proto3" json:"processing_digest,omitempty"`
	TraceTag             string                 `protobuf:"bytes,14,opt,name=trace_tag,json=traceTag,proto3" json:"trace_tag,omitempty"`
	ProcessedAtUnix      int64                  `protobuf:"varint,15,opt,name=processed_at_unix,json=processedAtUnix,proto3" json:"processed_at_unix,omitempty"`
	unknownFields        protoimpl.UnknownFields
	sizeCache            protoimpl.SizeCache
}

func (x *OrderQuoteResponse) Reset()         { resetMsg(x, 3) }
func (x *OrderQuoteResponse) String() string { return protoimpl.X.MessageStringOf(x) }
func (*OrderQuoteResponse) ProtoMessage()    {}
func (x *OrderQuoteResponse) ProtoReflect() protoreflect.Message {
	return reflectMsg(x, 3)
}

// 4. CreditEvalRequest (OpCode: 0x21)
type CreditEvalRequest struct {
	state               protoimpl.MessageState `protogen:"open.v1"`
	UserId              string                 `protobuf:"bytes,1,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`
	RequestedLimitCents int64                  `protobuf:"varint,2,opt,name=requested_limit_cents,json=requestedLimitCents,proto3" json:"requested_limit_cents,omitempty"`
	AnnualIncomeCents   int64                  `protobuf:"varint,3,opt,name=annual_income_cents,json=annualIncomeCents,proto3" json:"annual_income_cents,omitempty"`
	ExistingDebtCents   int64                  `protobuf:"varint,4,opt,name=existing_debt_cents,json=existingDebtCents,proto3" json:"existing_debt_cents,omitempty"`
	ClientTimestamp     int64                  `protobuf:"varint,5,opt,name=client_timestamp,json=clientTimestamp,proto3" json:"client_timestamp,omitempty"`
	unknownFields       protoimpl.UnknownFields
	sizeCache           protoimpl.SizeCache
}

func (x *CreditEvalRequest) Reset()         { resetMsg(x, 4) }
func (x *CreditEvalRequest) String() string { return protoimpl.X.MessageStringOf(x) }
func (*CreditEvalRequest) ProtoMessage()    {}
func (x *CreditEvalRequest) ProtoReflect() protoreflect.Message {
	return reflectMsg(x, 4)
}

// 5. CreditEvalResponse (OpCode: 0x22)
type CreditEvalResponse struct {
	state              protoimpl.MessageState `protogen:"open.v1"`
	BizCode            int32                  `protobuf:"varint,1,opt,name=biz_code,json=bizCode,proto3" json:"biz_code,omitempty"`
	BizMessage         string                 `protobuf:"bytes,2,opt,name=biz_message,json=bizMessage,proto3" json:"biz_message,omitempty"`
	CreditGrade        string                 `protobuf:"bytes,3,opt,name=credit_grade,json=creditGrade,proto3" json:"credit_grade,omitempty"`
	DtiBps             int32                  `protobuf:"varint,4,opt,name=dti_bps,json=dtiBps,proto3" json:"dti_bps,omitempty"`
	MaxAllowableCents  int64                  `protobuf:"varint,5,opt,name=max_allowable_cents,json=maxAllowableCents,proto3" json:"max_allowable_cents,omitempty"`
	ApprovedLimitCents int64                  `protobuf:"varint,6,opt,name=approved_limit_cents,json=approvedLimitCents,proto3" json:"approved_limit_cents,omitempty"`
	Decision           string                 `protobuf:"bytes,7,opt,name=decision,proto3" json:"decision,omitempty"`
	ProcessingDigest   string                 `protobuf:"bytes,8,opt,name=processing_digest,json=processingDigest,proto3" json:"processing_digest,omitempty"`
	TraceTag           string                 `protobuf:"bytes,9,opt,name=trace_tag,json=traceTag,proto3" json:"trace_tag,omitempty"`
	ProcessedAtUnix    int64                  `protobuf:"varint,10,opt,name=processed_at_unix,json=processedAtUnix,proto3" json:"processed_at_unix,omitempty"`
	unknownFields      protoimpl.UnknownFields
	sizeCache          protoimpl.SizeCache
}

func (x *CreditEvalResponse) Reset()         { resetMsg(x, 5) }
func (x *CreditEvalResponse) String() string { return protoimpl.X.MessageStringOf(x) }
func (*CreditEvalResponse) ProtoMessage()    {}
func (x *CreditEvalResponse) ProtoReflect() protoreflect.Message {
	return reflectMsg(x, 5)
}

// 6. BackendOrderQueryRequest (OpCode: 0x31)
type BackendOrderQueryRequest struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	SkuId         string                 `protobuf:"bytes,1,opt,name=sku_id,json=skuId,proto3" json:"sku_id,omitempty"`
	UserId        string                 `protobuf:"bytes,2,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`
	Region        string                 `protobuf:"bytes,3,opt,name=region,proto3" json:"region,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *BackendOrderQueryRequest) Reset()         { resetMsg(x, 6) }
func (x *BackendOrderQueryRequest) String() string { return protoimpl.X.MessageStringOf(x) }
func (*BackendOrderQueryRequest) ProtoMessage()    {}
func (x *BackendOrderQueryRequest) ProtoReflect() protoreflect.Message {
	return reflectMsg(x, 6)
}

// 7. BackendOrderQueryResponse (OpCode: 0x32)
type BackendOrderQueryResponse struct {
	state           protoimpl.MessageState `protogen:"open.v1"`
	Found           bool                   `protobuf:"varint,1,opt,name=found,proto3" json:"found,omitempty"`
	SkuId           string                 `protobuf:"bytes,2,opt,name=sku_id,json=skuId,proto3" json:"sku_id,omitempty"`
	SkuName         string                 `protobuf:"bytes,3,opt,name=sku_name,json=skuName,proto3" json:"sku_name,omitempty"`
	BasePriceCents  int64                  `protobuf:"varint,4,opt,name=base_price_cents,json=basePriceCents,proto3" json:"base_price_cents,omitempty"`
	AvailableStock  int32                  `protobuf:"varint,5,opt,name=available_stock,json=availableStock,proto3" json:"available_stock,omitempty"`
	UnitWeightGrams int32                  `protobuf:"varint,6,opt,name=unit_weight_grams,json=unitWeightGrams,proto3" json:"unit_weight_grams,omitempty"`
	WarehouseCode   string                 `protobuf:"bytes,7,opt,name=warehouse_code,json=warehouseCode,proto3" json:"warehouse_code,omitempty"`
	UserCreditScore int32                  `protobuf:"varint,8,opt,name=user_credit_score,json=userCreditScore,proto3" json:"user_credit_score,omitempty"`
	RiskBlacklisted bool                   `protobuf:"varint,9,opt,name=risk_blacklisted,json=riskBlacklisted,proto3" json:"risk_blacklisted,omitempty"`
	unknownFields   protoimpl.UnknownFields
	sizeCache       protoimpl.SizeCache
}

func (x *BackendOrderQueryResponse) Reset()         { resetMsg(x, 7) }
func (x *BackendOrderQueryResponse) String() string { return protoimpl.X.MessageStringOf(x) }
func (*BackendOrderQueryResponse) ProtoMessage()    {}
func (x *BackendOrderQueryResponse) ProtoReflect() protoreflect.Message {
	return reflectMsg(x, 7)
}

// 8. BackendCreditQueryRequest (OpCode: 0x41)
type BackendCreditQueryRequest struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	UserId        string                 `protobuf:"bytes,1,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *BackendCreditQueryRequest) Reset()         { resetMsg(x, 8) }
func (x *BackendCreditQueryRequest) String() string { return protoimpl.X.MessageStringOf(x) }
func (*BackendCreditQueryRequest) ProtoMessage()    {}
func (x *BackendCreditQueryRequest) ProtoReflect() protoreflect.Message {
	return reflectMsg(x, 8)
}

// 9. BackendCreditQueryResponse (OpCode: 0x42)
type BackendCreditQueryResponse struct {
	state                  protoimpl.MessageState `protogen:"open.v1"`
	Found                  bool                   `protobuf:"varint,1,opt,name=found,proto3" json:"found,omitempty"`
	UserId                 string                 `protobuf:"bytes,2,opt,name=user_id,json=userId,proto3" json:"user_id,omitempty"`
	CreditScore            int32                  `protobuf:"varint,3,opt,name=credit_score,json=creditScore,proto3" json:"credit_score,omitempty"`
	HistoricalDefaultCount int32                  `protobuf:"varint,4,opt,name=historical_default_count,json=historicalDefaultCount,proto3" json:"historical_default_count,omitempty"`
	AccountAgeMonths       int32                  `protobuf:"varint,5,opt,name=account_age_months,json=accountAgeMonths,proto3" json:"account_age_months,omitempty"`
	RiskBlacklisted        bool                   `protobuf:"varint,6,opt,name=risk_blacklisted,json=riskBlacklisted,proto3" json:"risk_blacklisted,omitempty"`
	unknownFields          protoimpl.UnknownFields
	sizeCache              protoimpl.SizeCache
}

func (x *BackendCreditQueryResponse) Reset()         { resetMsg(x, 9) }
func (x *BackendCreditQueryResponse) String() string { return protoimpl.X.MessageStringOf(x) }
func (*BackendCreditQueryResponse) ProtoMessage()    {}
func (x *BackendCreditQueryResponse) ProtoReflect() protoreflect.Message {
	return reflectMsg(x, 9)
}

// 10. ErrorResponse (OpCode: 0xFF)
type ErrorResponse struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	ErrorCode     int32                  `protobuf:"varint,1,opt,name=error_code,json=errorCode,proto3" json:"error_code,omitempty"`
	ErrorMessage  string                 `protobuf:"bytes,2,opt,name=error_message,json=errorMessage,proto3" json:"error_message,omitempty"`
	RequestId     uint64                 `protobuf:"varint,3,opt,name=request_id,json=requestId,proto3" json:"request_id,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *ErrorResponse) Reset()         { resetMsg(x, 10) }
func (x *ErrorResponse) String() string { return protoimpl.X.MessageStringOf(x) }
func (*ErrorResponse) ProtoMessage()    {}
func (x *ErrorResponse) ProtoReflect() protoreflect.Message {
	return reflectMsg(x, 10)
}

var (
	File_flowbench_v1_messages_proto protoreflect.FileDescriptor
	msgTypes                         = make([]protoimpl.MessageInfo, 11)
	initOnce                         sync.Once
)

func resetMsg[T any](x *T, idx int) {
	ensureInit()
	*x = *new(T)
	mi := &msgTypes[idx]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func reflectMsg[T any](x *T, idx int) protoreflect.Message {
	ensureInit()
	mi := &msgTypes[idx]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

func fieldDesc(name string, num int32, typ descriptorpb.FieldDescriptorProto_Type, jsonName string) *descriptorpb.FieldDescriptorProto {
	label := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL
	return &descriptorpb.FieldDescriptorProto{
		Name:     proto.String(name),
		Number:   proto.Int32(num),
		Label:    &label,
		Type:     &typ,
		JsonName: proto.String(jsonName),
	}
}

func init() {
	ensureInit()
}

func ensureInit() {
	initOnce.Do(func() {
		const (
			tString = descriptorpb.FieldDescriptorProto_TYPE_STRING
			tInt32  = descriptorpb.FieldDescriptorProto_TYPE_INT32
			tInt64  = descriptorpb.FieldDescriptorProto_TYPE_INT64
			tUint64 = descriptorpb.FieldDescriptorProto_TYPE_UINT64
			tBool   = descriptorpb.FieldDescriptorProto_TYPE_BOOL
		)

		fdp := &descriptorpb.FileDescriptorProto{
			Syntax:  proto.String("proto3"),
			Name:    proto.String("flowbench/v1/messages.proto"),
			Package: proto.String("flowbench.v1"),
			Options: &descriptorpb.FileOptions{
				GoPackage: proto.String("github.com/fugui/flowbench/internal/protocol/pb;pb"),
			},
			MessageType: []*descriptorpb.DescriptorProto{
				{
					Name: proto.String("PingRequest"),
					Field: []*descriptorpb.FieldDescriptorProto{
						fieldDesc("client_id", 1, tString, "clientId"),
						fieldDesc("message", 2, tString, "message"),
						fieldDesc("sent_at_unix", 3, tInt64, "sentAtUnix"),
					},
				},
				{
					Name: proto.String("PongResponse"),
					Field: []*descriptorpb.FieldDescriptorProto{
						fieldDesc("biz_code", 1, tInt32, "bizCode"),
						fieldDesc("server_id", 2, tString, "serverId"),
						fieldDesc("echo_message", 3, tString, "echoMessage"),
						fieldDesc("status", 4, tString, "status"),
						fieldDesc("replied_at_unix", 5, tInt64, "repliedAtUnix"),
					},
				},
				{
					Name: proto.String("OrderQuoteRequest"),
					Field: []*descriptorpb.FieldDescriptorProto{
						fieldDesc("user_id", 1, tString, "userId"),
						fieldDesc("sku_id", 2, tString, "skuId"),
						fieldDesc("quantity", 3, tInt32, "quantity"),
						fieldDesc("vip_level", 4, tInt32, "vipLevel"),
						fieldDesc("region", 5, tString, "region"),
						fieldDesc("client_timestamp", 6, tInt64, "clientTimestamp"),
					},
				},
				{
					Name: proto.String("OrderQuoteResponse"),
					Field: []*descriptorpb.FieldDescriptorProto{
						fieldDesc("biz_code", 1, tInt32, "bizCode"),
						fieldDesc("biz_message", 2, tString, "bizMessage"),
						fieldDesc("sku_name", 3, tString, "skuName"),
						fieldDesc("unit_price_cents", 4, tInt64, "unitPriceCents"),
						fieldDesc("subtotal_cents", 5, tInt64, "subtotalCents"),
						fieldDesc("discount_cents", 6, tInt64, "discountCents"),
						fieldDesc("discounted_cents", 7, tInt64, "discountedCents"),
						fieldDesc("tax_cents", 8, tInt64, "taxCents"),
						fieldDesc("shipping_cents", 9, tInt64, "shippingCents"),
						fieldDesc("final_amount_cents", 10, tInt64, "finalAmountCents"),
						fieldDesc("risk_decision", 11, tString, "riskDecision"),
						fieldDesc("fulfillment_warehouse", 12, tString, "fulfillmentWarehouse"),
						fieldDesc("processing_digest", 13, tString, "processingDigest"),
						fieldDesc("trace_tag", 14, tString, "traceTag"),
						fieldDesc("processed_at_unix", 15, tInt64, "processedAtUnix"),
					},
				},
				{
					Name: proto.String("CreditEvalRequest"),
					Field: []*descriptorpb.FieldDescriptorProto{
						fieldDesc("user_id", 1, tString, "userId"),
						fieldDesc("requested_limit_cents", 2, tInt64, "requestedLimitCents"),
						fieldDesc("annual_income_cents", 3, tInt64, "annualIncomeCents"),
						fieldDesc("existing_debt_cents", 4, tInt64, "existingDebtCents"),
						fieldDesc("client_timestamp", 5, tInt64, "clientTimestamp"),
					},
				},
				{
					Name: proto.String("CreditEvalResponse"),
					Field: []*descriptorpb.FieldDescriptorProto{
						fieldDesc("biz_code", 1, tInt32, "bizCode"),
						fieldDesc("biz_message", 2, tString, "bizMessage"),
						fieldDesc("credit_grade", 3, tString, "creditGrade"),
						fieldDesc("dti_bps", 4, tInt32, "dtiBps"),
						fieldDesc("max_allowable_cents", 5, tInt64, "maxAllowableCents"),
						fieldDesc("approved_limit_cents", 6, tInt64, "approvedLimitCents"),
						fieldDesc("decision", 7, tString, "decision"),
						fieldDesc("processing_digest", 8, tString, "processingDigest"),
						fieldDesc("trace_tag", 9, tString, "traceTag"),
						fieldDesc("processed_at_unix", 10, tInt64, "processedAtUnix"),
					},
				},
				{
					Name: proto.String("BackendOrderQueryRequest"),
					Field: []*descriptorpb.FieldDescriptorProto{
						fieldDesc("sku_id", 1, tString, "skuId"),
						fieldDesc("user_id", 2, tString, "userId"),
						fieldDesc("region", 3, tString, "region"),
					},
				},
				{
					Name: proto.String("BackendOrderQueryResponse"),
					Field: []*descriptorpb.FieldDescriptorProto{
						fieldDesc("found", 1, tBool, "found"),
						fieldDesc("sku_id", 2, tString, "skuId"),
						fieldDesc("sku_name", 3, tString, "skuName"),
						fieldDesc("base_price_cents", 4, tInt64, "basePriceCents"),
						fieldDesc("available_stock", 5, tInt32, "availableStock"),
						fieldDesc("unit_weight_grams", 6, tInt32, "unitWeightGrams"),
						fieldDesc("warehouse_code", 7, tString, "warehouseCode"),
						fieldDesc("user_credit_score", 8, tInt32, "userCreditScore"),
						fieldDesc("risk_blacklisted", 9, tBool, "riskBlacklisted"),
					},
				},
				{
					Name: proto.String("BackendCreditQueryRequest"),
					Field: []*descriptorpb.FieldDescriptorProto{
						fieldDesc("user_id", 1, tString, "userId"),
					},
				},
				{
					Name: proto.String("BackendCreditQueryResponse"),
					Field: []*descriptorpb.FieldDescriptorProto{
						fieldDesc("found", 1, tBool, "found"),
						fieldDesc("user_id", 2, tString, "userId"),
						fieldDesc("credit_score", 3, tInt32, "creditScore"),
						fieldDesc("historical_default_count", 4, tInt32, "historicalDefaultCount"),
						fieldDesc("account_age_months", 5, tInt32, "accountAgeMonths"),
						fieldDesc("risk_blacklisted", 6, tBool, "riskBlacklisted"),
					},
				},
				{
					Name: proto.String("ErrorResponse"),
					Field: []*descriptorpb.FieldDescriptorProto{
						fieldDesc("error_code", 1, tInt32, "errorCode"),
						fieldDesc("error_message", 2, tString, "errorMessage"),
						fieldDesc("request_id", 3, tUint64, "requestId"),
					},
				},
			},
		}

		rawDesc, err := proto.Marshal(fdp)
		if err != nil {
			panic("failed to marshal flowbench FileDescriptorProto: " + err.Error())
		}

		type x struct{}
		out := protoimpl.TypeBuilder{
			File: protoimpl.DescBuilder{
				GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
				RawDescriptor: rawDesc,
				NumEnums:      0,
				NumMessages:   11,
				NumExtensions: 0,
				NumServices:   0,
			},
			GoTypes: []any{
				(*PingRequest)(nil),
				(*PongResponse)(nil),
				(*OrderQuoteRequest)(nil),
				(*OrderQuoteResponse)(nil),
				(*CreditEvalRequest)(nil),
				(*CreditEvalResponse)(nil),
				(*BackendOrderQueryRequest)(nil),
				(*BackendOrderQueryResponse)(nil),
				(*BackendCreditQueryRequest)(nil),
				(*BackendCreditQueryResponse)(nil),
				(*ErrorResponse)(nil),
			},
			DependencyIndexes: []int32{0, 0, 0, 0, 0},
			MessageInfos:      msgTypes,
		}.Build()
		File_flowbench_v1_messages_proto = out.File
	})
}
