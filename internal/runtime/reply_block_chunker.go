package runtime

import "strings"

type ReplyBlockChunker struct {
	lastEmitted string
}

func NewReplyBlockChunker() *ReplyBlockChunker {
	return &ReplyBlockChunker{}
}

func (c *ReplyBlockChunker) OnText(text string) (string, bool) {
	if c == nil {
		return text, text != ""
	}
	if strings.TrimSpace(text) == "" || text == c.lastEmitted {
		return "", false
	}
	if shouldBufferReplyBlockText(text) {
		return "", false
	}
	c.lastEmitted = text
	return text, true
}

func (c *ReplyBlockChunker) Flush(text string) (string, bool) {
	if c == nil {
		return text, text != ""
	}
	if strings.TrimSpace(text) == "" || text == c.lastEmitted {
		return "", false
	}
	c.lastEmitted = text
	return text, true
}

func shouldBufferReplyBlockText(text string) bool {
	if text == "" {
		return false
	}
	if strings.Count(text, "```")%2 != 0 {
		return true
	}
	if looksLikeMarkdownLeadInOnly(text) {
		return true
	}
	return false
}

func looksLikeMarkdownLeadInOnly(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	parts := strings.SplitN(text, "\n\n", 2)
	if len(parts) != 2 {
		return false
	}
	if strings.TrimSpace(parts[1]) != "" {
		return false
	}
	firstLine := strings.TrimSpace(strings.SplitN(trimmed, "\n", 2)[0])
	if firstLine == "" {
		return false
	}
	return strings.HasPrefix(firstLine, "#") ||
		strings.HasPrefix(firstLine, ">") ||
		strings.HasPrefix(firstLine, "- ") ||
		strings.HasPrefix(firstLine, "* ")
}
