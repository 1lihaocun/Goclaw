package runtime

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"goclaw/internal/domain"
)

type ReplyProjectorConfig struct {
	ShowReasoning           bool
	ToolLifecycleVisibility string
	RoomKind                domain.RoomKind
}

type ReplyProjector struct {
	config                ReplyProjectorConfig
	visibleText           string
	finalText             string
	pendingHiddenBoundary bool
	deliveredFinal        bool
}

func NewReplyProjector(config ReplyProjectorConfig) *ReplyProjector {
	return &ReplyProjector{config: config}
}

func (p *ReplyProjector) OnRunEvent(event RunEvent) ([]ReplyDelivery, error) {
	if p == nil {
		return nil, nil
	}
	switch event.Type {
	case RunEventAssistantDelta:
		return p.appendVisibleText(event.Text), nil
	case RunEventReasoningDelta:
		if p.config.ShowReasoning {
			return p.appendVisibleText(event.Text), nil
		}
		p.markHiddenBoundary()
		return nil, nil
	case RunEventAssistantBlock:
		p.markHiddenBoundary()
		return nil, nil
	case RunEventToolCallStarted:
		return p.projectToolEvent(event, "started", false), nil
	case RunEventToolResult:
		return p.projectToolEvent(event, "result", true), nil
	case RunEventToolCallFinished:
		return p.projectToolEvent(event, "finished", true), nil
	case RunEventFinalText:
		return p.projectFinalText(event.Text), nil
	case RunEventRunCompleted, RunEventRunFailed:
		deliveries, err := p.Flush()
		p.reset()
		return deliveries, err
	default:
		return nil, nil
	}
}

func (p *ReplyProjector) Flush() ([]ReplyDelivery, error) {
	if p == nil || p.deliveredFinal {
		return nil, nil
	}
	replyText := p.currentText()
	if strings.TrimSpace(replyText) == "" {
		return nil, nil
	}
	p.finalText = replyText
	p.deliveredFinal = true
	return []ReplyDelivery{{
		Kind: ReplyDeliveryFinal,
		Text: replyText,
	}}, nil
}

func (p *ReplyProjector) ReplyText() string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(p.currentText())
}

func (p *ReplyProjector) CurrentText() string {
	if p == nil {
		return ""
	}
	return p.currentText()
}

func (p *ReplyProjector) appendVisibleText(text string) []ReplyDelivery {
	if p == nil || text == "" {
		return nil
	}
	if p.pendingHiddenBoundary {
		if separator := hiddenBoundarySeparator(p.visibleText, text); separator != "" {
			p.visibleText += separator
		}
		p.pendingHiddenBoundary = false
	}
	p.visibleText += text
	return []ReplyDelivery{{
		Kind: ReplyDeliveryText,
		Text: p.visibleText,
	}}
}

func (p *ReplyProjector) projectToolEvent(
	event RunEvent,
	status string,
	allowEdit bool,
) []ReplyDelivery {
	if p == nil {
		return nil
	}
	p.markHiddenBoundary()
	if !p.config.showToolLifecycle() {
		return nil
	}
	return []ReplyDelivery{{
		Kind:           ReplyDeliveryTool,
		Text:           strings.TrimSpace(event.Text),
		ToolCallID:     event.ToolCallID,
		ToolName:       event.ToolName,
		ToolStatus:     status,
		ToolArgsJSON:   event.ToolArgsJSON,
		ToolResultJSON: event.ToolResultJSON,
		ErrorText:      event.ErrorText,
		AllowEdit:      allowEdit,
		NativeToolCall: event.NativeToolCall,
		Iteration:      event.Iteration,
	}}
}

func (p *ReplyProjector) projectFinalText(text string) []ReplyDelivery {
	if p == nil {
		return nil
	}
	replyText := firstNonEmptyStreamingText(text, p.currentText())
	if strings.TrimSpace(replyText) == "" {
		return nil
	}
	p.finalText = replyText
	p.visibleText = replyText
	p.pendingHiddenBoundary = false
	p.deliveredFinal = true
	return []ReplyDelivery{{
		Kind: ReplyDeliveryFinal,
		Text: replyText,
	}}
}

func (p *ReplyProjector) currentText() string {
	return firstNonEmptyStreamingText(p.finalText, p.visibleText)
}

func (p *ReplyProjector) markHiddenBoundary() {
	if p == nil || strings.TrimSpace(p.currentText()) == "" {
		return
	}
	p.pendingHiddenBoundary = true
}

func (p *ReplyProjector) reset() {
	if p == nil {
		return
	}
	p.visibleText = ""
	p.finalText = ""
	p.pendingHiddenBoundary = false
	p.deliveredFinal = false
}

func hiddenBoundarySeparator(current string, next string) string {
	if current == "" || next == "" {
		return ""
	}
	last, ok := lastRune(current)
	if !ok || unicode.IsSpace(last) || suppressTrailingBoundarySeparator(last) {
		return ""
	}
	first, ok := firstRune(next)
	if !ok || unicode.IsSpace(first) || suppressLeadingBoundarySeparator(first) {
		return ""
	}
	return " "
}

func lastRune(value string) (rune, bool) {
	r, size := utf8.DecodeLastRuneInString(value)
	if r == utf8.RuneError && size == 0 {
		return 0, false
	}
	return r, true
}

func firstRune(value string) (rune, bool) {
	r, size := utf8.DecodeRuneInString(value)
	if r == utf8.RuneError && size == 0 {
		return 0, false
	}
	return r, true
}

func suppressTrailingBoundarySeparator(r rune) bool {
	return strings.ContainsRune("([{<'\"/\\", r)
}

func suppressLeadingBoundarySeparator(r rune) bool {
	return strings.ContainsRune(",.;:!?)]}>%/'\"", r)
}

func normalizeToolLifecycleVisibility(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "all":
		return "all"
	case "dm_only":
		return "dm_only"
	default:
		return "off"
	}
}

func (c ReplyProjectorConfig) showToolLifecycle() bool {
	switch normalizeToolLifecycleVisibility(c.ToolLifecycleVisibility) {
	case "all":
		return true
	case "dm_only":
		return c.RoomKind == domain.RoomKindDirect
	default:
		return false
	}
}
