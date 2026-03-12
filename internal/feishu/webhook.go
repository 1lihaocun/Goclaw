package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"goclaw/internal/domain"
)

const defaultWebhookMaxBodyBytes int64 = 1 << 20

var ErrUnsupportedEncryptedEvent = errors.New("feishu: encrypted webhook events are not supported")

type EventHeader struct {
	EventID    string `json:"event_id,omitempty"`
	EventType  string `json:"event_type"`
	AppID      string `json:"app_id,omitempty"`
	TenantKey  string `json:"tenant_key,omitempty"`
	CreateTime string `json:"create_time,omitempty"`
	Token      string `json:"token,omitempty"`
}

type EventEnvelope struct {
	Schema    string          `json:"schema,omitempty"`
	Header    *EventHeader    `json:"header,omitempty"`
	Event     json.RawMessage `json:"event,omitempty"`
	Token     string          `json:"token,omitempty"`
	Type      string          `json:"type,omitempty"`
	Challenge string          `json:"challenge,omitempty"`
	Encrypt   string          `json:"encrypt,omitempty"`
}

type MessageEventIngestor interface {
	IngestFeishuMessageEvent(ctx context.Context, profileID domain.ProfileID, event MessageEvent) error
}

type EventObserver interface {
	ObserveFeishuEvent(ctx context.Context, profileID domain.ProfileID, event Event) error
}

type WebhookHandlerParams struct {
	ProfileID         domain.ProfileID
	EncryptKey        string
	VerificationToken string
	MaxBodyBytes      int64
	Ingestor          MessageEventIngestor
	EventObserver     EventObserver
}

func NewWebhookHandler(params WebhookHandlerParams) http.Handler {
	maxBodyBytes := params.MaxBodyBytes
	if maxBodyBytes <= 0 {
		maxBodyBytes = defaultWebhookMaxBodyBytes
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		if !isJSONRequest(r.Header.Get("Content-Type")) {
			http.Error(w, "Unsupported Media Type", http.StatusUnsupportedMediaType)
			return
		}

		body, err := readRequestBody(w, r, maxBodyBytes)
		if err != nil {
			return
		}

		var envelope EventEnvelope
		if err := json.Unmarshal(body, &envelope); err != nil {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}

		if strings.TrimSpace(params.EncryptKey) != "" {
			if strings.TrimSpace(envelope.Encrypt) != "" {
				plainText, err := decryptEncryptedPayload(params.EncryptKey, envelope.Encrypt)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				if err := json.Unmarshal(plainText, &envelope); err != nil {
					http.Error(w, "Bad Request", http.StatusBadRequest)
					return
				}
			}

			if strings.TrimSpace(envelope.Type) != "url_verification" {
				if err := verifyRequestSignature(
					r.Header.Get("X-Lark-Request-Timestamp"),
					r.Header.Get("X-Lark-Request-Nonce"),
					params.EncryptKey,
					body,
					r.Header.Get("X-Lark-Signature"),
				); err != nil {
					http.Error(w, err.Error(), http.StatusUnauthorized)
					return
				}
			}
		}

		if params.VerificationToken != "" &&
			strings.TrimSpace(envelope.Token) != params.VerificationToken {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		switch strings.TrimSpace(envelope.Type) {
		case "url_verification":
			writeJSON(w, http.StatusOK, map[string]string{
				"challenge": envelope.Challenge,
			})
			return
		case "":
			// Fall through to event-type dispatch below.
		default:
			// Some payloads set type=event_callback; we dispatch on header.event_type.
		}

		eventType := ""
		if envelope.Header != nil {
			eventType = strings.TrimSpace(envelope.Header.EventType)
		}

		switch eventType {
		case "im.message.receive_v1":
			if params.Ingestor == nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}

			var event MessageEvent
			if err := json.Unmarshal(envelope.Event, &event); err != nil {
				http.Error(w, "Bad Request", http.StatusBadRequest)
				return
			}
			if err := params.Ingestor.IngestFeishuMessageEvent(r.Context(), params.ProfileID, event); err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, map[string]int{"code": 0})
			return
		case "im.message.recalled_v1",
			"im.message.reaction.created_v1",
			"im.message.reaction.deleted_v1",
			"im.chat.member.bot.added_v1",
			"im.chat.member.bot.deleted_v1",
			"im.chat.member.user.added_v1",
			"im.chat.member.user.deleted_v1",
			"im.chat.member.user.withdrawn_v1":
			if params.EventObserver == nil {
				writeJSON(w, http.StatusAccepted, map[string]any{
					"code":    0,
					"ignored": true,
				})
				return
			}

			event, err := decodeEvent(envelope.Header, envelope.Event)
			if err != nil {
				http.Error(w, "Bad Request", http.StatusBadRequest)
				return
			}
			if err := params.EventObserver.ObserveFeishuEvent(r.Context(), params.ProfileID, event); err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, map[string]int{"code": 0})
			return
		case "":
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		default:
			writeJSON(w, http.StatusAccepted, map[string]any{
				"code":    0,
				"ignored": true,
			})
			return
		}
	})
}

func isJSONRequest(contentType string) bool {
	value := strings.TrimSpace(strings.ToLower(contentType))
	return strings.HasPrefix(value, "application/json")
}

func readRequestBody(w http.ResponseWriter, r *http.Request, maxBodyBytes int64) ([]byte, error) {
	reader := http.MaxBytesReader(w, r.Body, maxBodyBytes)
	defer reader.Close()

	body, err := io.ReadAll(reader)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			http.Error(w, "Request Entity Too Large", http.StatusRequestEntityTooLarge)
			return nil, err
		}
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return nil, err
	}
	return body, nil
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}
