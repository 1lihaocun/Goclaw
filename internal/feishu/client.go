package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const tokenSkew = 60 * time.Second

var ErrNotConfigured = errors.New("feishu client not configured")

type SendResult struct {
	MessageID string
	ChatID    string
	MsgType   string
}

type DocumentRawContent struct {
	DocumentID   string `json:"document_id"`
	DocumentType string `json:"document_type"`
	Content      string `json:"content"`
}

type WikiNodeInfo struct {
	SpaceID         string `json:"space_id,omitempty"`
	NodeToken       string `json:"node_token,omitempty"`
	ObjToken        string `json:"obj_token,omitempty"`
	ObjType         string `json:"obj_type,omitempty"`
	ParentNodeToken string `json:"parent_node_token,omitempty"`
	OriginNodeToken string `json:"origin_node_token,omitempty"`
	Title           string `json:"title,omitempty"`
	HasChild        bool   `json:"has_child,omitempty"`
}

type SendMessageRequest struct {
	ReceiveIDType string
	ReceiveID     string
	MsgType       string
	ContentJSON   string
	UUID          string
}

type ReplyMessageRequest struct {
	MessageID     string
	MsgType       string
	ContentJSON   string
	ReplyInThread bool
	UUID          string
}

type UpdateMessageRequest struct {
	MessageID   string
	MsgType     string
	ContentJSON string
}

type ForwardMessageRequest struct {
	MessageID     string
	ReceiveIDType string
	ReceiveID     string
}

type MergeForwardMessagesRequest struct {
	ReceiveIDType string
	ReceiveID     string
	MessageIDs    []string
}

type ThreadForwardRequest struct {
	ThreadID      string
	ReceiveIDType string
	ReceiveID     string
}

type PushFollowUpRequest struct {
	MessageID     string
	FollowUpsJSON string
}

type UrgentMessageRequest struct {
	MessageID  string
	UserIDType string
	UserIDs    []string
}

type UrgentResult struct {
	InvalidUserIDs []string `json:"invalid_user_ids,omitempty"`
}

type ClientConfig struct {
	BaseURL    string
	AppID      string
	AppSecret  string
	HTTPClient *http.Client
}

type Client struct {
	baseURL    string
	appID      string
	appSecret  string
	httpClient *http.Client

	mu          sync.Mutex
	accessToken string
	expiresAt   time.Time
}

type ChatInfo struct {
	ChatID      string `json:"chat_id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Avatar      string `json:"avatar,omitempty"`
	OwnerID     string `json:"owner_id,omitempty"`
	OwnerIDType string `json:"owner_id_type,omitempty"`
	ChatMode    string `json:"chat_mode,omitempty"`
	ChatType    string `json:"chat_type,omitempty"`
	TenantKey   string `json:"tenant_key,omitempty"`
	UserCount   int    `json:"user_count,omitempty"`
	BotCount    int    `json:"bot_count,omitempty"`
	External    bool   `json:"external,omitempty"`
}

type ChatMember struct {
	MemberIDType string `json:"member_id_type,omitempty"`
	MemberID     string `json:"member_id,omitempty"`
	Name         string `json:"name,omitempty"`
	TenantKey    string `json:"tenant_key,omitempty"`
}

type ChatMembersPage struct {
	Items       []ChatMember `json:"items"`
	PageToken   string       `json:"page_token,omitempty"`
	HasMore     bool         `json:"has_more"`
	MemberTotal int          `json:"member_total,omitempty"`
}

type MessageReactionInfo struct {
	ReactionID   string `json:"reaction_id,omitempty"`
	EmojiType    string `json:"emoji_type,omitempty"`
	OperatorID   string `json:"operator_id,omitempty"`
	OperatorType string `json:"operator_type,omitempty"`
	ActionTime   string `json:"action_time,omitempty"`
}

type MessageReactionsPage struct {
	Items     []MessageReactionInfo `json:"items"`
	PageToken string                `json:"page_token,omitempty"`
	HasMore   bool                  `json:"has_more"`
}

type PinInfo struct {
	MessageID      string `json:"message_id,omitempty"`
	ChatID         string `json:"chat_id,omitempty"`
	OperatorID     string `json:"operator_id,omitempty"`
	OperatorIDType string `json:"operator_id_type,omitempty"`
	CreateTime     string `json:"create_time,omitempty"`
}

type AppScope struct {
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"`
	GrantStatus int    `json:"grant_status"`
	Granted     bool   `json:"granted"`
}

type AppScopes struct {
	Granted []AppScope `json:"granted"`
	Pending []AppScope `json:"pending"`
	Summary string     `json:"summary"`
}

type PinsPage struct {
	Items     []PinInfo `json:"items"`
	PageToken string    `json:"page_token,omitempty"`
	HasMore   bool      `json:"has_more"`
}

func NewClient(cfg ClientConfig) *Client {
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{
		baseURL:    strings.TrimRight(cfg.BaseURL, "/"),
		appID:      strings.TrimSpace(cfg.AppID),
		appSecret:  strings.TrimSpace(cfg.AppSecret),
		httpClient: httpClient,
	}
}

func (c *Client) Configured() bool {
	return c != nil && c.baseURL != "" && c.appID != "" && c.appSecret != ""
}

func TextContentJSON(text string) string {
	return string(mustJSON(map[string]string{"text": text}))
}

func (c *Client) SendText(ctx context.Context, chatID, text string) (SendResult, error) {
	return c.SendMessage(ctx, SendMessageRequest{
		ReceiveIDType: "chat_id",
		ReceiveID:     chatID,
		MsgType:       "text",
		ContentJSON:   TextContentJSON(text),
	})
}

func (c *Client) SendPost(ctx context.Context, chatID, contentJSON string) (SendResult, error) {
	return c.SendMessage(ctx, SendMessageRequest{
		ReceiveIDType: "chat_id",
		ReceiveID:     chatID,
		MsgType:       "post",
		ContentJSON:   contentJSON,
	})
}

func (c *Client) SendMessage(ctx context.Context, req SendMessageRequest) (SendResult, error) {
	receiveIDType := strings.TrimSpace(req.ReceiveIDType)
	if receiveIDType == "" {
		receiveIDType = "chat_id"
	}
	receiveID := strings.TrimSpace(req.ReceiveID)
	if receiveID == "" {
		return SendResult{}, fmt.Errorf("missing receive id")
	}
	msgType := strings.TrimSpace(req.MsgType)
	if msgType == "" {
		return SendResult{}, fmt.Errorf("missing msg type")
	}
	contentJSON := strings.TrimSpace(req.ContentJSON)
	if !json.Valid([]byte(contentJSON)) {
		return SendResult{}, fmt.Errorf("invalid message content json")
	}

	payload := map[string]any{
		"receive_id": receiveID,
		"msg_type":   msgType,
		"content":    contentJSON,
	}
	if uuid := strings.TrimSpace(req.UUID); uuid != "" {
		payload["uuid"] = uuid
	}
	body, err := c.callJSON(ctx, http.MethodPost, "/im/v1/messages?receive_id_type="+url.QueryEscape(receiveIDType), payload)
	if err != nil {
		return SendResult{}, err
	}
	return decodeSendResult(body, "feishu send failed", receiveID, msgType)
}

func (c *Client) ReplyMessage(ctx context.Context, req ReplyMessageRequest) (SendResult, error) {
	messageID := strings.TrimSpace(req.MessageID)
	if messageID == "" {
		return SendResult{}, fmt.Errorf("missing message id")
	}
	msgType := strings.TrimSpace(req.MsgType)
	if msgType == "" {
		return SendResult{}, fmt.Errorf("missing msg type")
	}
	contentJSON := strings.TrimSpace(req.ContentJSON)
	if !json.Valid([]byte(contentJSON)) {
		return SendResult{}, fmt.Errorf("invalid reply content json")
	}
	payload := map[string]any{
		"msg_type": msgType,
		"content":  contentJSON,
	}
	if req.ReplyInThread {
		payload["reply_in_thread"] = true
	}
	if uuid := strings.TrimSpace(req.UUID); uuid != "" {
		payload["uuid"] = uuid
	}
	body, err := c.callJSON(ctx, http.MethodPost, "/im/v1/messages/"+url.PathEscape(messageID)+"/reply", payload)
	if err != nil {
		return SendResult{}, err
	}
	return decodeSendResult(body, "feishu reply failed", "", msgType)
}

func (c *Client) ForwardMessage(ctx context.Context, req ForwardMessageRequest) (SendResult, error) {
	messageID := strings.TrimSpace(req.MessageID)
	if messageID == "" {
		return SendResult{}, fmt.Errorf("missing message id")
	}
	receiveIDType := strings.TrimSpace(req.ReceiveIDType)
	if receiveIDType == "" {
		receiveIDType = "chat_id"
	}
	receiveID := strings.TrimSpace(req.ReceiveID)
	if receiveID == "" {
		return SendResult{}, fmt.Errorf("missing receive id")
	}
	body, err := c.callJSON(
		ctx,
		http.MethodPost,
		"/im/v1/messages/"+url.PathEscape(messageID)+"/forward?receive_id_type="+url.QueryEscape(receiveIDType),
		map[string]any{"receive_id": receiveID},
	)
	if err != nil {
		return SendResult{}, err
	}
	return decodeSendResult(body, "feishu forward failed", receiveID, "")
}

func (c *Client) MergeForwardMessages(ctx context.Context, req MergeForwardMessagesRequest) (SendResult, error) {
	receiveIDType := strings.TrimSpace(req.ReceiveIDType)
	if receiveIDType == "" {
		receiveIDType = "chat_id"
	}
	receiveID := strings.TrimSpace(req.ReceiveID)
	if receiveID == "" {
		return SendResult{}, fmt.Errorf("missing receive id")
	}
	messageIDs := make([]string, 0, len(req.MessageIDs))
	for _, messageID := range req.MessageIDs {
		messageID = strings.TrimSpace(messageID)
		if messageID == "" {
			continue
		}
		messageIDs = append(messageIDs, messageID)
	}
	if len(messageIDs) == 0 {
		return SendResult{}, fmt.Errorf("missing message ids")
	}
	body, err := c.callJSON(
		ctx,
		http.MethodPost,
		"/im/v1/messages/merge_forward?receive_id_type="+url.QueryEscape(receiveIDType),
		map[string]any{"receive_id": receiveID, "message_id_list": messageIDs},
	)
	if err != nil {
		return SendResult{}, err
	}
	return decodeSendResult(body, "feishu merge forward failed", receiveID, "merge_forward")
}

func (c *Client) ForwardThread(ctx context.Context, req ThreadForwardRequest) (SendResult, error) {
	threadID := strings.TrimSpace(req.ThreadID)
	if threadID == "" {
		return SendResult{}, fmt.Errorf("missing thread id")
	}
	receiveIDType := strings.TrimSpace(req.ReceiveIDType)
	if receiveIDType == "" {
		receiveIDType = "chat_id"
	}
	receiveID := strings.TrimSpace(req.ReceiveID)
	if receiveID == "" {
		return SendResult{}, fmt.Errorf("missing receive id")
	}
	body, err := c.callJSON(
		ctx,
		http.MethodPost,
		"/im/v1/threads/"+url.PathEscape(threadID)+"/forward?receive_id_type="+url.QueryEscape(receiveIDType),
		map[string]any{"receive_id": receiveID},
	)
	if err != nil {
		return SendResult{}, err
	}
	return decodeSendResult(body, "feishu thread forward failed", receiveID, "")
}

func (c *Client) PushFollowUp(ctx context.Context, req PushFollowUpRequest) error {
	messageID := strings.TrimSpace(req.MessageID)
	if messageID == "" {
		return fmt.Errorf("missing message id")
	}
	followUpsJSON := strings.TrimSpace(req.FollowUpsJSON)
	if followUpsJSON == "" {
		return fmt.Errorf("missing follow ups json")
	}
	var followUps []any
	if err := json.Unmarshal([]byte(followUpsJSON), &followUps); err != nil {
		return fmt.Errorf("invalid follow ups json: %w", err)
	}
	body, err := c.callJSON(
		ctx,
		http.MethodPost,
		"/im/v1/messages/"+url.PathEscape(messageID)+"/push_follow_up",
		map[string]any{"follow_ups": followUps},
	)
	if err != nil {
		return err
	}
	return decodeCodeOnly(body, "feishu push follow up failed")
}

func (c *Client) UrgentApp(ctx context.Context, req UrgentMessageRequest) (UrgentResult, error) {
	return c.urgentMessage(ctx, "/urgent_app", "feishu urgent app failed", req)
}

func (c *Client) UrgentPhone(ctx context.Context, req UrgentMessageRequest) (UrgentResult, error) {
	return c.urgentMessage(ctx, "/urgent_phone", "feishu urgent phone failed", req)
}

func (c *Client) UrgentSms(ctx context.Context, req UrgentMessageRequest) (UrgentResult, error) {
	return c.urgentMessage(ctx, "/urgent_sms", "feishu urgent sms failed", req)
}

func (c *Client) urgentMessage(ctx context.Context, suffix string, operation string, req UrgentMessageRequest) (UrgentResult, error) {
	messageID := strings.TrimSpace(req.MessageID)
	if messageID == "" {
		return UrgentResult{}, fmt.Errorf("missing message id")
	}
	userIDType := strings.TrimSpace(req.UserIDType)
	if userIDType == "" {
		userIDType = "open_id"
	}
	userIDs := make([]string, 0, len(req.UserIDs))
	for _, userID := range req.UserIDs {
		userID = strings.TrimSpace(userID)
		if userID == "" {
			continue
		}
		userIDs = append(userIDs, userID)
	}
	if len(userIDs) == 0 {
		return UrgentResult{}, fmt.Errorf("missing user ids")
	}
	body, err := c.callJSON(
		ctx,
		http.MethodPatch,
		"/im/v1/messages/"+url.PathEscape(messageID)+suffix+"?user_id_type="+url.QueryEscape(userIDType),
		map[string]any{"user_id_list": userIDs},
	)
	if err != nil {
		return UrgentResult{}, err
	}
	return decodeUrgentResult(body, operation)
}

func (c *Client) GetChat(ctx context.Context, chatID string) (ChatInfo, error) {
	path := "/im/v1/chats/" + url.PathEscape(strings.TrimSpace(chatID)) + "?user_id_type=open_id"
	body, err := c.callJSON(ctx, http.MethodGet, path, nil)
	if err != nil {
		return ChatInfo{}, err
	}

	var response struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Avatar      string `json:"avatar"`
			Name        string `json:"name"`
			Description string `json:"description"`
			OwnerID     string `json:"owner_id"`
			OwnerIDType string `json:"owner_id_type"`
			ChatMode    string `json:"chat_mode"`
			ChatType    string `json:"chat_type"`
			TenantKey   string `json:"tenant_key"`
			UserCount   string `json:"user_count"`
			BotCount    string `json:"bot_count"`
			External    bool   `json:"external"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return ChatInfo{}, err
	}
	if response.Code != 0 {
		return ChatInfo{}, newAPIError("feishu get chat failed", response.Code, response.Msg, body)
	}

	return ChatInfo{
		ChatID:      strings.TrimSpace(chatID),
		Name:        response.Data.Name,
		Description: response.Data.Description,
		Avatar:      response.Data.Avatar,
		OwnerID:     response.Data.OwnerID,
		OwnerIDType: response.Data.OwnerIDType,
		ChatMode:    response.Data.ChatMode,
		ChatType:    response.Data.ChatType,
		TenantKey:   response.Data.TenantKey,
		UserCount:   atoiOrZero(response.Data.UserCount),
		BotCount:    atoiOrZero(response.Data.BotCount),
		External:    response.Data.External,
	}, nil
}

func (c *Client) ListChatMembers(
	ctx context.Context,
	chatID string,
	pageSize int,
	pageToken string,
) (ChatMembersPage, error) {
	query := url.Values{}
	query.Set("member_id_type", "open_id")
	if pageSize > 0 {
		query.Set("page_size", strconv.Itoa(pageSize))
	}
	if strings.TrimSpace(pageToken) != "" {
		query.Set("page_token", strings.TrimSpace(pageToken))
	}

	path := "/im/v1/chats/" + url.PathEscape(strings.TrimSpace(chatID)) + "/members"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}

	body, err := c.callJSON(ctx, http.MethodGet, path, nil)
	if err != nil {
		return ChatMembersPage{}, err
	}

	var response struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Items []struct {
				MemberIDType string `json:"member_id_type"`
				MemberID     string `json:"member_id"`
				Name         string `json:"name"`
				TenantKey    string `json:"tenant_key"`
			} `json:"items"`
			PageToken   string `json:"page_token"`
			HasMore     bool   `json:"has_more"`
			MemberTotal int    `json:"member_total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return ChatMembersPage{}, err
	}
	if response.Code != 0 {
		return ChatMembersPage{}, newAPIError("feishu list chat members failed", response.Code, response.Msg, body)
	}

	items := make([]ChatMember, 0, len(response.Data.Items))
	for _, item := range response.Data.Items {
		items = append(items, ChatMember{
			MemberIDType: item.MemberIDType,
			MemberID:     item.MemberID,
			Name:         item.Name,
			TenantKey:    item.TenantKey,
		})
	}

	return ChatMembersPage{
		Items:       items,
		PageToken:   response.Data.PageToken,
		HasMore:     response.Data.HasMore,
		MemberTotal: response.Data.MemberTotal,
	}, nil
}

func (c *Client) UpdateMessage(ctx context.Context, req UpdateMessageRequest) error {
	body, err := c.callJSON(ctx, http.MethodPut, "/im/v1/messages/"+url.PathEscape(strings.TrimSpace(req.MessageID)), map[string]string{
		"msg_type": normalizeEditableMessageType(req.MsgType),
		"content":  strings.TrimSpace(req.ContentJSON),
	})
	if err != nil {
		return err
	}

	var response struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return err
	}
	if response.Code != 0 {
		return newAPIError("feishu update message failed", response.Code, response.Msg, body)
	}
	return nil
}

func normalizeEditableMessageType(msgType string) string {
	switch strings.ToLower(strings.TrimSpace(msgType)) {
	case "post":
		return "post"
	default:
		return "text"
	}
}

func (c *Client) RecallMessage(ctx context.Context, messageID string) error {
	body, err := c.callJSON(ctx, http.MethodDelete, "/im/v1/messages/"+url.PathEscape(strings.TrimSpace(messageID)), nil)
	if err != nil {
		return err
	}

	var response struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return err
	}
	if response.Code != 0 {
		return newAPIError("feishu recall message failed", response.Code, response.Msg, body)
	}
	return nil
}

func (c *Client) GetDocumentRawContent(ctx context.Context, documentID string) (DocumentRawContent, error) {
	body, err := c.callJSON(ctx, http.MethodGet, "/docx/v1/documents/"+url.PathEscape(strings.TrimSpace(documentID))+"/raw_content", nil)
	if err != nil {
		return DocumentRawContent{}, err
	}

	var response struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Content string `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return DocumentRawContent{}, err
	}
	if response.Code != 0 {
		return DocumentRawContent{}, newAPIError("feishu get document raw content failed", response.Code, response.Msg, body)
	}
	return DocumentRawContent{DocumentID: strings.TrimSpace(documentID), DocumentType: "docx", Content: strings.TrimSpace(response.Data.Content)}, nil
}

func (c *Client) GetLegacyDocumentRawContent(ctx context.Context, documentID string) (DocumentRawContent, error) {
	body, err := c.callJSON(ctx, http.MethodGet, "/doc/v2/"+url.PathEscape(strings.TrimSpace(documentID))+"/raw_content", nil)
	if err != nil {
		return DocumentRawContent{}, err
	}

	var response struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Content string `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return DocumentRawContent{}, err
	}
	if response.Code != 0 {
		return DocumentRawContent{}, newAPIError("feishu get legacy document raw content failed", response.Code, response.Msg, body)
	}
	return DocumentRawContent{DocumentID: strings.TrimSpace(documentID), DocumentType: "doc", Content: strings.TrimSpace(response.Data.Content)}, nil
}

func (c *Client) GetWikiNode(ctx context.Context, nodeToken string) (WikiNodeInfo, error) {
	path := "/wiki/v2/spaces/get_node?token=" + url.QueryEscape(strings.TrimSpace(nodeToken))
	body, err := c.callJSON(ctx, http.MethodGet, path, nil)
	if err != nil {
		return WikiNodeInfo{}, err
	}

	var response struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Node struct {
				SpaceID         string `json:"space_id"`
				NodeToken       string `json:"node_token"`
				ObjToken        string `json:"obj_token"`
				ObjType         string `json:"obj_type"`
				ParentNodeToken string `json:"parent_node_token"`
				OriginNodeToken string `json:"origin_node_token"`
				Title           string `json:"title"`
				HasChild        bool   `json:"has_child"`
			} `json:"node"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return WikiNodeInfo{}, err
	}
	if response.Code != 0 {
		return WikiNodeInfo{}, newAPIError("feishu get wiki node failed", response.Code, response.Msg, body)
	}
	return WikiNodeInfo{
		SpaceID:         strings.TrimSpace(response.Data.Node.SpaceID),
		NodeToken:       strings.TrimSpace(response.Data.Node.NodeToken),
		ObjToken:        strings.TrimSpace(response.Data.Node.ObjToken),
		ObjType:         strings.TrimSpace(response.Data.Node.ObjType),
		ParentNodeToken: strings.TrimSpace(response.Data.Node.ParentNodeToken),
		OriginNodeToken: strings.TrimSpace(response.Data.Node.OriginNodeToken),
		Title:           strings.TrimSpace(response.Data.Node.Title),
		HasChild:        response.Data.Node.HasChild,
	}, nil
}

func (c *Client) ListAppScopes(ctx context.Context) (AppScopes, error) {
	body, err := c.callJSON(ctx, http.MethodGet, "/application/v6/scopes", nil)
	if err != nil {
		return AppScopes{}, err
	}

	var response struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Scopes []struct {
				ScopeName   string `json:"scope_name"`
				GrantStatus int    `json:"grant_status"`
				ScopeType   string `json:"scope_type"`
			} `json:"scopes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return AppScopes{}, err
	}
	if response.Code != 0 {
		return AppScopes{}, newAPIError("feishu list app scopes failed", response.Code, response.Msg, body)
	}

	granted := make([]AppScope, 0, len(response.Data.Scopes))
	pending := make([]AppScope, 0, len(response.Data.Scopes))
	for _, item := range response.Data.Scopes {
		scope := AppScope{
			Name:        strings.TrimSpace(item.ScopeName),
			Type:        strings.TrimSpace(item.ScopeType),
			GrantStatus: item.GrantStatus,
			Granted:     item.GrantStatus == 1,
		}
		if scope.Granted {
			granted = append(granted, scope)
		} else {
			pending = append(pending, scope)
		}
	}

	return AppScopes{
		Granted: granted,
		Pending: pending,
		Summary: fmt.Sprintf("%d granted, %d pending", len(granted), len(pending)),
	}, nil
}

func (c *Client) AddMessageReaction(
	ctx context.Context,
	messageID string,
	emojiType string,
) (MessageReactionInfo, error) {
	body, err := c.callJSON(ctx, http.MethodPost, "/im/v1/messages/"+url.PathEscape(strings.TrimSpace(messageID))+"/reactions", map[string]any{
		"reaction_type": map[string]string{
			"emoji_type": strings.TrimSpace(emojiType),
		},
	})
	if err != nil {
		return MessageReactionInfo{}, err
	}

	return decodeReactionOperation(body, "feishu add reaction failed")
}

func (c *Client) RemoveMessageReaction(
	ctx context.Context,
	messageID string,
	reactionID string,
) (MessageReactionInfo, error) {
	body, err := c.callJSON(
		ctx,
		http.MethodDelete,
		"/im/v1/messages/"+url.PathEscape(strings.TrimSpace(messageID))+"/reactions/"+url.PathEscape(strings.TrimSpace(reactionID)),
		nil,
	)
	if err != nil {
		return MessageReactionInfo{}, err
	}

	return decodeReactionOperation(body, "feishu delete reaction failed")
}

func (c *Client) ListMessageReactions(
	ctx context.Context,
	messageID string,
	reactionType string,
	pageSize int,
	pageToken string,
) (MessageReactionsPage, error) {
	query := url.Values{}
	query.Set("user_id_type", "open_id")
	if strings.TrimSpace(reactionType) != "" {
		query.Set("reaction_type", strings.TrimSpace(reactionType))
	}
	if pageSize > 0 {
		query.Set("page_size", strconv.Itoa(pageSize))
	}
	if strings.TrimSpace(pageToken) != "" {
		query.Set("page_token", strings.TrimSpace(pageToken))
	}

	path := "/im/v1/messages/" + url.PathEscape(strings.TrimSpace(messageID)) + "/reactions"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}

	body, err := c.callJSON(ctx, http.MethodGet, path, nil)
	if err != nil {
		return MessageReactionsPage{}, err
	}

	var response struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Items []struct {
				ReactionID string `json:"reaction_id"`
				ActionTime string `json:"action_time"`
				Operator   struct {
					OperatorID   string `json:"operator_id"`
					OperatorType string `json:"operator_type"`
				} `json:"operator"`
				ReactionType struct {
					EmojiType string `json:"emoji_type"`
				} `json:"reaction_type"`
			} `json:"items"`
			PageToken string `json:"page_token"`
			HasMore   bool   `json:"has_more"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return MessageReactionsPage{}, err
	}
	if response.Code != 0 {
		return MessageReactionsPage{}, newAPIError("feishu list message reactions failed", response.Code, response.Msg, body)
	}

	items := make([]MessageReactionInfo, 0, len(response.Data.Items))
	for _, item := range response.Data.Items {
		items = append(items, MessageReactionInfo{
			ReactionID:   item.ReactionID,
			EmojiType:    item.ReactionType.EmojiType,
			OperatorID:   item.Operator.OperatorID,
			OperatorType: item.Operator.OperatorType,
			ActionTime:   item.ActionTime,
		})
	}

	return MessageReactionsPage{
		Items:     items,
		PageToken: response.Data.PageToken,
		HasMore:   response.Data.HasMore,
	}, nil
}

func (c *Client) PinMessage(ctx context.Context, messageID string) (PinInfo, error) {
	body, err := c.callJSON(ctx, http.MethodPost, "/im/v1/pins", map[string]string{
		"message_id": strings.TrimSpace(messageID),
	})
	if err != nil {
		return PinInfo{}, err
	}

	var response struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Pin struct {
				MessageID      string `json:"message_id"`
				ChatID         string `json:"chat_id"`
				OperatorID     string `json:"operator_id"`
				OperatorIDType string `json:"operator_id_type"`
				CreateTime     string `json:"create_time"`
			} `json:"pin"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return PinInfo{}, err
	}
	if response.Code != 0 {
		return PinInfo{}, newAPIError("feishu pin message failed", response.Code, response.Msg, body)
	}

	return PinInfo{
		MessageID:      response.Data.Pin.MessageID,
		ChatID:         response.Data.Pin.ChatID,
		OperatorID:     response.Data.Pin.OperatorID,
		OperatorIDType: response.Data.Pin.OperatorIDType,
		CreateTime:     response.Data.Pin.CreateTime,
	}, nil
}

func (c *Client) UnpinMessage(ctx context.Context, messageID string) error {
	body, err := c.callJSON(ctx, http.MethodDelete, "/im/v1/pins/"+url.PathEscape(strings.TrimSpace(messageID)), nil)
	if err != nil {
		return err
	}

	var response struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return err
	}
	if response.Code != 0 {
		return newAPIError("feishu unpin message failed", response.Code, response.Msg, body)
	}
	return nil
}

func (c *Client) ListPins(
	ctx context.Context,
	chatID string,
	pageSize int,
	pageToken string,
) (PinsPage, error) {
	query := url.Values{}
	query.Set("chat_id", strings.TrimSpace(chatID))
	if pageSize > 0 {
		query.Set("page_size", strconv.Itoa(pageSize))
	}
	if strings.TrimSpace(pageToken) != "" {
		query.Set("page_token", strings.TrimSpace(pageToken))
	}

	path := "/im/v1/pins?" + query.Encode()
	body, err := c.callJSON(ctx, http.MethodGet, path, nil)
	if err != nil {
		return PinsPage{}, err
	}

	var response struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Items []struct {
				MessageID      string `json:"message_id"`
				ChatID         string `json:"chat_id"`
				OperatorID     string `json:"operator_id"`
				OperatorIDType string `json:"operator_id_type"`
				CreateTime     string `json:"create_time"`
			} `json:"items"`
			PageToken string `json:"page_token"`
			HasMore   bool   `json:"has_more"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return PinsPage{}, err
	}
	if response.Code != 0 {
		return PinsPage{}, newAPIError("feishu list pins failed", response.Code, response.Msg, body)
	}

	items := make([]PinInfo, 0, len(response.Data.Items))
	for _, item := range response.Data.Items {
		items = append(items, PinInfo{
			MessageID:      item.MessageID,
			ChatID:         item.ChatID,
			OperatorID:     item.OperatorID,
			OperatorIDType: item.OperatorIDType,
			CreateTime:     item.CreateTime,
		})
	}

	return PinsPage{
		Items:     items,
		PageToken: response.Data.PageToken,
		HasMore:   response.Data.HasMore,
	}, nil
}

func decodeCodeOnly(body []byte, operation string) error {
	var response struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return err
	}
	if response.Code != 0 {
		return newAPIError(operation, response.Code, response.Msg, body)
	}
	return nil
}

func decodeSendResult(body []byte, operation, fallbackChatID, fallbackMsgType string) (SendResult, error) {
	var response struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			MessageID string `json:"message_id"`
			ChatID    string `json:"chat_id"`
			MsgType   string `json:"msg_type"`
			Message   *struct {
				MessageID string `json:"message_id"`
				ChatID    string `json:"chat_id"`
				MsgType   string `json:"msg_type"`
			} `json:"message"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return SendResult{}, err
	}
	if response.Code != 0 {
		return SendResult{}, newAPIError(operation, response.Code, response.Msg, body)
	}
	result := SendResult{
		MessageID: strings.TrimSpace(response.Data.MessageID),
		ChatID:    strings.TrimSpace(response.Data.ChatID),
		MsgType:   strings.TrimSpace(response.Data.MsgType),
	}
	if response.Data.Message != nil {
		if result.MessageID == "" {
			result.MessageID = strings.TrimSpace(response.Data.Message.MessageID)
		}
		if result.ChatID == "" {
			result.ChatID = strings.TrimSpace(response.Data.Message.ChatID)
		}
		if result.MsgType == "" {
			result.MsgType = strings.TrimSpace(response.Data.Message.MsgType)
		}
	}
	if result.ChatID == "" {
		result.ChatID = strings.TrimSpace(fallbackChatID)
	}
	if result.MsgType == "" {
		result.MsgType = strings.TrimSpace(fallbackMsgType)
	}
	return result, nil
}

func decodeUrgentResult(body []byte, operation string) (UrgentResult, error) {
	var response struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			InvalidUserIDList []string `json:"invalid_user_id_list"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return UrgentResult{}, err
	}
	if response.Code != 0 {
		return UrgentResult{}, newAPIError(operation, response.Code, response.Msg, body)
	}
	result := UrgentResult{InvalidUserIDs: make([]string, 0, len(response.Data.InvalidUserIDList))}
	for _, userID := range response.Data.InvalidUserIDList {
		userID = strings.TrimSpace(userID)
		if userID == "" {
			continue
		}
		result.InvalidUserIDs = append(result.InvalidUserIDs, userID)
	}
	return result, nil
}

func decodeReactionOperation(body []byte, message string) (MessageReactionInfo, error) {
	var response struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			ReactionID string `json:"reaction_id"`
			ActionTime string `json:"action_time"`
			Operator   struct {
				OperatorID   string `json:"operator_id"`
				OperatorType string `json:"operator_type"`
			} `json:"operator"`
			ReactionType struct {
				EmojiType string `json:"emoji_type"`
			} `json:"reaction_type"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return MessageReactionInfo{}, err
	}
	if response.Code != 0 {
		return MessageReactionInfo{}, newAPIError(message, response.Code, response.Msg, body)
	}

	return MessageReactionInfo{
		ReactionID:   response.Data.ReactionID,
		EmojiType:    response.Data.ReactionType.EmojiType,
		OperatorID:   response.Data.Operator.OperatorID,
		OperatorType: response.Data.Operator.OperatorType,
		ActionTime:   response.Data.ActionTime,
	}, nil
}

func (c *Client) callJSON(ctx context.Context, method, path string, payload any) ([]byte, error) {
	if !c.Configured() {
		return nil, ErrNotConfigured
	}

	token, err := c.tenantAccessToken(ctx)
	if err != nil {
		return nil, err
	}

	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(mustJSON(payload))
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	responseBody, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, &RequestError{
			Method:     method,
			Path:       path,
			StatusCode: res.StatusCode,
			Body:       cloneBytes(responseBody),
		}
	}
	return responseBody, nil
}

func (c *Client) tenantAccessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.accessToken != "" && time.Now().Before(c.expiresAt) {
		token := c.accessToken
		c.mu.Unlock()
		return token, nil
	}
	c.mu.Unlock()

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+"/auth/v3/tenant_access_token/internal",
		bytes.NewReader(mustJSON(map[string]string{
			"app_id":     c.appID,
			"app_secret": c.appSecret,
		})),
	)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return "", err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", &RequestError{
			Method:     http.MethodPost,
			Path:       "/auth/v3/tenant_access_token/internal",
			StatusCode: res.StatusCode,
			Body:       cloneBytes(body),
		}
	}

	var response struct {
		Code              int    `json:"code"`
		Msg               string `json:"msg"`
		TenantAccessToken string `json:"tenant_access_token"`
		Expire            int    `json:"expire"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", err
	}
	if response.Code != 0 || strings.TrimSpace(response.TenantAccessToken) == "" {
		return "", newAPIError("feishu auth failed", response.Code, response.Msg, body)
	}

	expiresAt := time.Now().Add(time.Duration(response.Expire) * time.Second).Add(-tokenSkew)
	c.mu.Lock()
	c.accessToken = response.TenantAccessToken
	c.expiresAt = expiresAt
	c.mu.Unlock()
	return response.TenantAccessToken, nil
}

func mustJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

func atoiOrZero(value string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return parsed
}
