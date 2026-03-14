package model

import (
	"bufio"
	"io"
	"strings"
)

type SSEEvent struct {
	Event string
	Data  string
}

func ConsumeSSE(
	reader io.Reader,
	handler func(SSEEvent) error,
) error {
	buffered := bufio.NewReader(reader)
	eventName := ""
	dataLines := make([]string, 0)

	dispatch := func() error {
		if len(dataLines) == 0 && strings.TrimSpace(eventName) == "" {
			return nil
		}
		event := SSEEvent{
			Event: strings.TrimSpace(eventName),
			Data:  strings.Join(dataLines, "\n"),
		}
		eventName = ""
		dataLines = dataLines[:0]
		return handler(event)
	}

	for {
		line, err := buffered.ReadString('\n')
		if err != nil && err != io.EOF {
			return err
		}
		if err == io.EOF && line == "" {
			break
		}

		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			if dispatchErr := dispatch(); dispatchErr != nil {
				return dispatchErr
			}
		} else if strings.HasPrefix(trimmed, ":") {
			// Ignore SSE comments/heartbeats.
		} else if strings.HasPrefix(trimmed, "event:") {
			eventName = strings.TrimSpace(trimmed[len("event:"):])
		} else if strings.HasPrefix(trimmed, "data:") {
			value := trimmed[len("data:"):]
			if strings.HasPrefix(value, " ") {
				value = value[1:]
			}
			dataLines = append(dataLines, value)
		}

		if err == io.EOF {
			break
		}
	}
	return dispatch()
}
