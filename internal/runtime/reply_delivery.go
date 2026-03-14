package runtime

type ReplyDeliveryKind string

const (
	ReplyDeliveryText  ReplyDeliveryKind = "text"
	ReplyDeliveryTool  ReplyDeliveryKind = "tool"
	ReplyDeliveryFinal ReplyDeliveryKind = "final"
)

type ReplyDelivery struct {
	Kind           ReplyDeliveryKind
	Text           string
	ToolCallID     string
	ToolName       string
	ToolStatus     string
	ToolArgsJSON   string
	ToolResultJSON string
	ErrorText      string
	AllowEdit      bool
	NativeToolCall bool
	Iteration      int
}
