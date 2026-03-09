package metrics

type ErrorReason string

const (
	ReasonDialFailed          ErrorReason = "dial_failed"
	ReasonHandshakeFailed     ErrorReason = "handshake_failed"
	ReasonWriteFailed         ErrorReason = "write_failed"
	ReasonReadFailed          ErrorReason = "read_failed"
	ReasonRequestCreateFailed ErrorReason = "request_create_failed"
	ReasonRequestSendFailed   ErrorReason = "request_send_failed"
	ReasonRPCFailed           ErrorReason = "rpc_failed"
	ReasonMarshalFailed       ErrorReason = "marshal_failed"
)

func RecordGeneratorError(flowType, source, target string, reason ErrorReason) {
	GeneratorErrors.WithLabelValues(flowType, source, target, string(reason)).Inc()
}
