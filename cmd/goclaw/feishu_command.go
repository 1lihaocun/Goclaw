package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	feishuchannel "goclaw/internal/channels/feishu"
	"goclaw/internal/domain"
	rawfeishu "goclaw/internal/feishu"
	"goclaw/internal/runtime"
)

func runFeishuCommand(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: goclaw feishu <chat|message|thread|reaction|pin|scopes|capabilities> [flags]")
	}

	switch args[0] {
	case "chat":
		return runFeishuChatCommand(ctx, app, args[1:], stdout)
	case "message":
		return runFeishuMessageCommand(ctx, app, args[1:], stdout)
	case "thread":
		return runFeishuThreadCommand(ctx, app, args[1:], stdout)
	case "reaction":
		return runFeishuReactionCommand(ctx, app, args[1:], stdout)
	case "pin":
		return runFeishuPinCommand(ctx, app, args[1:], stdout)
	case "scopes":
		return runFeishuScopesCommand(ctx, app, args[1:], stdout)
	case "capabilities":
		return runFeishuCapabilitiesCommand(ctx, app, args[1:], stdout)
	default:
		return fmt.Errorf("unknown feishu subcommand %q", args[0])
	}
}

func runFeishuChatCommand(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: goclaw feishu chat <get|members> [flags]")
	}

	switch args[0] {
	case "get":
		fs := flag.NewFlagSet("feishu chat get", flag.ContinueOnError)
		fs.SetOutput(io.Discard)

		var profileID string
		var chatID string
		fs.StringVar(&profileID, "profile-id", "", "profile id")
		fs.StringVar(&chatID, "chat-id", "", "chat id")

		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(profileID) == "" {
			return errors.New("missing required flag --profile-id")
		}
		if strings.TrimSpace(chatID) == "" {
			return errors.New("missing required flag --chat-id")
		}

		client, err := newFeishuClient(app, profileID)
		if err != nil {
			return err
		}
		chat, err := client.GetChat(ctx, chatID)
		if err != nil {
			return decorateFeishuError(err)
		}
		return writeJSON(stdout, chat)

	case "members":
		fs := flag.NewFlagSet("feishu chat members", flag.ContinueOnError)
		fs.SetOutput(io.Discard)

		var profileID string
		var chatID string
		var pageSize int
		var pageToken string
		fs.StringVar(&profileID, "profile-id", "", "profile id")
		fs.StringVar(&chatID, "chat-id", "", "chat id")
		fs.IntVar(&pageSize, "page-size", 50, "page size")
		fs.StringVar(&pageToken, "page-token", "", "page token")

		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(profileID) == "" {
			return errors.New("missing required flag --profile-id")
		}
		if strings.TrimSpace(chatID) == "" {
			return errors.New("missing required flag --chat-id")
		}

		client, err := newFeishuClient(app, profileID)
		if err != nil {
			return err
		}
		page, err := client.ListChatMembers(ctx, chatID, pageSize, pageToken)
		if err != nil {
			return decorateFeishuError(err)
		}
		return writeJSON(stdout, page)

	default:
		return fmt.Errorf("unknown feishu chat subcommand %q", args[0])
	}
}

func runFeishuMessageCommand(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: goclaw feishu message <send|reply|forward|merge-forward|follow-up|urgent-app|urgent-phone|urgent-sms|update|recall> [flags]")
	}

	switch args[0] {
	case "send":
		fs := flag.NewFlagSet("feishu message send", flag.ContinueOnError)
		fs.SetOutput(io.Discard)

		var profileID string
		var receiveID string
		var receiveIDType string
		var chatID string
		var msgType string
		var contentJSON string
		var text string
		var uuid string
		fs.StringVar(&profileID, "profile-id", "", "profile id")
		fs.StringVar(&receiveID, "receive-id", "", "receive id")
		fs.StringVar(&receiveIDType, "receive-id-type", "chat_id", "receive id type")
		fs.StringVar(&chatID, "chat-id", "", "chat id convenience alias for receive-id")
		fs.StringVar(&msgType, "msg-type", "text", "message type")
		fs.StringVar(&contentJSON, "content-json", "", "message content json")
		fs.StringVar(&text, "text", "", "text content convenience wrapper")
		fs.StringVar(&uuid, "uuid", "", "dedupe uuid")

		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(profileID) == "" {
			return errors.New("missing required flag --profile-id")
		}
		if strings.TrimSpace(receiveID) == "" {
			receiveID = strings.TrimSpace(chatID)
		}
		if strings.TrimSpace(receiveID) == "" {
			return errors.New("missing required flag --receive-id or --chat-id")
		}
		if strings.TrimSpace(contentJSON) == "" && strings.TrimSpace(text) == "" {
			return errors.New("missing required flag --content-json or --text")
		}
		if strings.TrimSpace(contentJSON) != "" && strings.TrimSpace(text) != "" {
			return errors.New("--content-json and --text cannot be used together")
		}
		msgType = strings.TrimSpace(strings.ToLower(msgType))
		if strings.TrimSpace(text) != "" {
			if msgType != "" && msgType != "text" {
				return errors.New("--text can only be used with --msg-type text")
			}
			msgType = "text"
			contentJSON = rawfeishu.TextContentJSON(text)
		}

		client, err := newFeishuClient(app, profileID)
		if err != nil {
			return err
		}
		result, err := client.SendMessage(ctx, rawfeishu.SendMessageRequest{
			ReceiveIDType: strings.TrimSpace(receiveIDType),
			ReceiveID:     strings.TrimSpace(receiveID),
			MsgType:       msgType,
			ContentJSON:   strings.TrimSpace(contentJSON),
			UUID:          strings.TrimSpace(uuid),
		})
		if err != nil {
			return decorateFeishuError(err)
		}
		return writeJSON(stdout, result)

	case "reply":
		fs := flag.NewFlagSet("feishu message reply", flag.ContinueOnError)
		fs.SetOutput(io.Discard)

		var profileID string
		var messageID string
		var msgType string
		var contentJSON string
		var text string
		var uuid string
		var replyInThread bool
		fs.StringVar(&profileID, "profile-id", "", "profile id")
		fs.StringVar(&messageID, "message-id", "", "message id")
		fs.StringVar(&msgType, "msg-type", "text", "message type")
		fs.StringVar(&contentJSON, "content-json", "", "message content json")
		fs.StringVar(&text, "text", "", "text content convenience wrapper")
		fs.StringVar(&uuid, "uuid", "", "dedupe uuid")
		fs.BoolVar(&replyInThread, "reply-in-thread", false, "reply in thread")

		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(profileID) == "" {
			return errors.New("missing required flag --profile-id")
		}
		if strings.TrimSpace(messageID) == "" {
			return errors.New("missing required flag --message-id")
		}
		if strings.TrimSpace(contentJSON) == "" && strings.TrimSpace(text) == "" {
			return errors.New("missing required flag --content-json or --text")
		}
		if strings.TrimSpace(contentJSON) != "" && strings.TrimSpace(text) != "" {
			return errors.New("--content-json and --text cannot be used together")
		}
		msgType = strings.TrimSpace(strings.ToLower(msgType))
		if strings.TrimSpace(text) != "" {
			if msgType != "" && msgType != "text" {
				return errors.New("--text can only be used with --msg-type text")
			}
			msgType = "text"
			contentJSON = rawfeishu.TextContentJSON(text)
		}

		client, err := newFeishuClient(app, profileID)
		if err != nil {
			return err
		}
		result, err := client.ReplyMessage(ctx, rawfeishu.ReplyMessageRequest{
			MessageID:     strings.TrimSpace(messageID),
			MsgType:       msgType,
			ContentJSON:   strings.TrimSpace(contentJSON),
			ReplyInThread: replyInThread,
			UUID:          strings.TrimSpace(uuid),
		})
		if err != nil {
			return decorateFeishuError(err)
		}
		return writeJSON(stdout, result)

	case "forward":
		fs := flag.NewFlagSet("feishu message forward", flag.ContinueOnError)
		fs.SetOutput(io.Discard)

		var profileID string
		var messageID string
		var receiveID string
		var receiveIDType string
		var chatID string
		fs.StringVar(&profileID, "profile-id", "", "profile id")
		fs.StringVar(&messageID, "message-id", "", "message id")
		fs.StringVar(&receiveID, "receive-id", "", "receive id")
		fs.StringVar(&receiveIDType, "receive-id-type", "chat_id", "receive id type")
		fs.StringVar(&chatID, "chat-id", "", "chat id convenience alias for receive-id")

		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(profileID) == "" {
			return errors.New("missing required flag --profile-id")
		}
		if strings.TrimSpace(messageID) == "" {
			return errors.New("missing required flag --message-id")
		}
		if strings.TrimSpace(receiveID) == "" {
			receiveID = strings.TrimSpace(chatID)
		}
		if strings.TrimSpace(receiveID) == "" {
			return errors.New("missing required flag --receive-id or --chat-id")
		}

		client, err := newFeishuClient(app, profileID)
		if err != nil {
			return err
		}
		result, err := client.ForwardMessage(ctx, rawfeishu.ForwardMessageRequest{MessageID: strings.TrimSpace(messageID), ReceiveIDType: strings.TrimSpace(receiveIDType), ReceiveID: strings.TrimSpace(receiveID)})
		if err != nil {
			return decorateFeishuError(err)
		}
		return writeJSON(stdout, result)

	case "merge-forward":
		fs := flag.NewFlagSet("feishu message merge-forward", flag.ContinueOnError)
		fs.SetOutput(io.Discard)

		var profileID string
		var messageIDs string
		var receiveID string
		var receiveIDType string
		var chatID string
		fs.StringVar(&profileID, "profile-id", "", "profile id")
		fs.StringVar(&messageIDs, "message-ids", "", "comma-separated message ids")
		fs.StringVar(&receiveID, "receive-id", "", "receive id")
		fs.StringVar(&receiveIDType, "receive-id-type", "chat_id", "receive id type")
		fs.StringVar(&chatID, "chat-id", "", "chat id convenience alias for receive-id")

		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(profileID) == "" {
			return errors.New("missing required flag --profile-id")
		}
		if strings.TrimSpace(messageIDs) == "" {
			return errors.New("missing required flag --message-ids")
		}
		if strings.TrimSpace(receiveID) == "" {
			receiveID = strings.TrimSpace(chatID)
		}
		if strings.TrimSpace(receiveID) == "" {
			return errors.New("missing required flag --receive-id or --chat-id")
		}

		client, err := newFeishuClient(app, profileID)
		if err != nil {
			return err
		}
		result, err := client.MergeForwardMessages(ctx, rawfeishu.MergeForwardMessagesRequest{ReceiveIDType: strings.TrimSpace(receiveIDType), ReceiveID: strings.TrimSpace(receiveID), MessageIDs: splitCommaSeparated(messageIDs)})
		if err != nil {
			return decorateFeishuError(err)
		}
		return writeJSON(stdout, result)

	case "follow-up":
		fs := flag.NewFlagSet("feishu message follow-up", flag.ContinueOnError)
		fs.SetOutput(io.Discard)

		var profileID string
		var messageID string
		var followUpsJSON string
		fs.StringVar(&profileID, "profile-id", "", "profile id")
		fs.StringVar(&messageID, "message-id", "", "message id")
		fs.StringVar(&followUpsJSON, "follow-ups-json", "", "follow ups json array")

		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(profileID) == "" {
			return errors.New("missing required flag --profile-id")
		}
		if strings.TrimSpace(messageID) == "" {
			return errors.New("missing required flag --message-id")
		}
		if strings.TrimSpace(followUpsJSON) == "" {
			return errors.New("missing required flag --follow-ups-json")
		}

		client, err := newFeishuClient(app, profileID)
		if err != nil {
			return err
		}
		if err := client.PushFollowUp(ctx, rawfeishu.PushFollowUpRequest{MessageID: strings.TrimSpace(messageID), FollowUpsJSON: strings.TrimSpace(followUpsJSON)}); err != nil {
			return decorateFeishuError(err)
		}
		return writeJSON(stdout, map[string]string{"profile_id": profileID, "message_id": strings.TrimSpace(messageID), "status": "follow_up_pushed"})

	case "urgent-app", "urgent-phone", "urgent-sms":
		fs := flag.NewFlagSet("feishu message urgent", flag.ContinueOnError)
		fs.SetOutput(io.Discard)

		var profileID string
		var messageID string
		var userIDType string
		var userIDs string
		fs.StringVar(&profileID, "profile-id", "", "profile id")
		fs.StringVar(&messageID, "message-id", "", "message id")
		fs.StringVar(&userIDType, "user-id-type", "open_id", "user id type")
		fs.StringVar(&userIDs, "user-ids", "", "comma-separated user ids")

		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(profileID) == "" {
			return errors.New("missing required flag --profile-id")
		}
		if strings.TrimSpace(messageID) == "" {
			return errors.New("missing required flag --message-id")
		}
		ids := splitCommaSeparated(userIDs)
		if len(ids) == 0 {
			return errors.New("missing required flag --user-ids")
		}

		client, err := newFeishuClient(app, profileID)
		if err != nil {
			return err
		}
		request := rawfeishu.UrgentMessageRequest{MessageID: strings.TrimSpace(messageID), UserIDType: strings.TrimSpace(userIDType), UserIDs: ids}
		var result rawfeishu.UrgentResult
		switch args[0] {
		case "urgent-app":
			result, err = client.UrgentApp(ctx, request)
		case "urgent-phone":
			result, err = client.UrgentPhone(ctx, request)
		default:
			result, err = client.UrgentSms(ctx, request)
		}
		if err != nil {
			return decorateFeishuError(err)
		}
		return writeJSON(stdout, result)

	case "update":
		fs := flag.NewFlagSet("feishu message update", flag.ContinueOnError)
		fs.SetOutput(io.Discard)

		var profileID string
		var messageID string
		var contentJSON string
		var text string
		fs.StringVar(&profileID, "profile-id", "", "profile id")
		fs.StringVar(&messageID, "message-id", "", "message id")
		fs.StringVar(&contentJSON, "content-json", "", "message content json")
		fs.StringVar(&text, "text", "", "text content convenience wrapper")

		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(profileID) == "" {
			return errors.New("missing required flag --profile-id")
		}
		if strings.TrimSpace(messageID) == "" {
			return errors.New("missing required flag --message-id")
		}
		if strings.TrimSpace(contentJSON) == "" && strings.TrimSpace(text) == "" {
			return errors.New("missing required flag --content-json or --text")
		}
		if strings.TrimSpace(contentJSON) != "" && strings.TrimSpace(text) != "" {
			return errors.New("--content-json and --text cannot be used together")
		}
		if strings.TrimSpace(text) != "" {
			contentJSON = rawfeishu.TextContentJSON(text)
		}

		client, err := newFeishuClient(app, profileID)
		if err != nil {
			return err
		}
		if err := client.UpdateMessageContent(ctx, messageID, contentJSON); err != nil {
			return decorateFeishuError(err)
		}
		return writeJSON(stdout, map[string]string{
			"profile_id": profileID,
			"message_id": messageID,
			"status":     "updated",
		})

	case "recall":
		fs := flag.NewFlagSet("feishu message recall", flag.ContinueOnError)
		fs.SetOutput(io.Discard)

		var profileID string
		var messageID string
		fs.StringVar(&profileID, "profile-id", "", "profile id")
		fs.StringVar(&messageID, "message-id", "", "message id")

		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(profileID) == "" {
			return errors.New("missing required flag --profile-id")
		}
		if strings.TrimSpace(messageID) == "" {
			return errors.New("missing required flag --message-id")
		}

		client, err := newFeishuClient(app, profileID)
		if err != nil {
			return err
		}
		if err := client.RecallMessage(ctx, messageID); err != nil {
			return decorateFeishuError(err)
		}
		return writeJSON(stdout, map[string]string{
			"profile_id": profileID,
			"message_id": messageID,
			"status":     "recalled",
		})

	default:
		return fmt.Errorf("unknown feishu message subcommand %q", args[0])
	}
}

func runFeishuThreadCommand(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: goclaw feishu thread <forward> [flags]")
	}

	switch args[0] {
	case "forward":
		fs := flag.NewFlagSet("feishu thread forward", flag.ContinueOnError)
		fs.SetOutput(io.Discard)

		var profileID string
		var threadID string
		var receiveID string
		var receiveIDType string
		var chatID string
		fs.StringVar(&profileID, "profile-id", "", "profile id")
		fs.StringVar(&threadID, "thread-id", "", "thread id")
		fs.StringVar(&receiveID, "receive-id", "", "receive id")
		fs.StringVar(&receiveIDType, "receive-id-type", "chat_id", "receive id type")
		fs.StringVar(&chatID, "chat-id", "", "chat id convenience alias for receive-id")

		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(profileID) == "" {
			return errors.New("missing required flag --profile-id")
		}
		if strings.TrimSpace(threadID) == "" {
			return errors.New("missing required flag --thread-id")
		}
		if strings.TrimSpace(receiveID) == "" {
			receiveID = strings.TrimSpace(chatID)
		}
		if strings.TrimSpace(receiveID) == "" {
			return errors.New("missing required flag --receive-id or --chat-id")
		}

		client, err := newFeishuClient(app, profileID)
		if err != nil {
			return err
		}
		result, err := client.ForwardThread(ctx, rawfeishu.ThreadForwardRequest{ThreadID: strings.TrimSpace(threadID), ReceiveIDType: strings.TrimSpace(receiveIDType), ReceiveID: strings.TrimSpace(receiveID)})
		if err != nil {
			return decorateFeishuError(err)
		}
		return writeJSON(stdout, result)

	default:
		return fmt.Errorf("unknown feishu thread subcommand %q", args[0])
	}
}

func runFeishuReactionCommand(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: goclaw feishu reaction <list|add|delete> [flags]")
	}

	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("feishu reaction list", flag.ContinueOnError)
		fs.SetOutput(io.Discard)

		var profileID string
		var messageID string
		var reactionType string
		var pageSize int
		var pageToken string
		fs.StringVar(&profileID, "profile-id", "", "profile id")
		fs.StringVar(&messageID, "message-id", "", "message id")
		fs.StringVar(&reactionType, "reaction-type", "", "reaction type")
		fs.IntVar(&pageSize, "page-size", 50, "page size")
		fs.StringVar(&pageToken, "page-token", "", "page token")

		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(profileID) == "" {
			return errors.New("missing required flag --profile-id")
		}
		if strings.TrimSpace(messageID) == "" {
			return errors.New("missing required flag --message-id")
		}

		client, err := newFeishuClient(app, profileID)
		if err != nil {
			return err
		}
		page, err := client.ListMessageReactions(ctx, messageID, reactionType, pageSize, pageToken)
		if err != nil {
			return decorateFeishuError(err)
		}
		return writeJSON(stdout, page)

	case "add":
		fs := flag.NewFlagSet("feishu reaction add", flag.ContinueOnError)
		fs.SetOutput(io.Discard)

		var profileID string
		var messageID string
		var emojiType string
		fs.StringVar(&profileID, "profile-id", "", "profile id")
		fs.StringVar(&messageID, "message-id", "", "message id")
		fs.StringVar(&emojiType, "emoji-type", "", "emoji type")

		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(profileID) == "" {
			return errors.New("missing required flag --profile-id")
		}
		if strings.TrimSpace(messageID) == "" {
			return errors.New("missing required flag --message-id")
		}
		if strings.TrimSpace(emojiType) == "" {
			return errors.New("missing required flag --emoji-type")
		}

		client, err := newFeishuClient(app, profileID)
		if err != nil {
			return err
		}
		reaction, err := client.AddMessageReaction(ctx, messageID, emojiType)
		if err != nil {
			return decorateFeishuError(err)
		}
		return writeJSON(stdout, reaction)

	case "delete":
		fs := flag.NewFlagSet("feishu reaction delete", flag.ContinueOnError)
		fs.SetOutput(io.Discard)

		var profileID string
		var messageID string
		var reactionID string
		fs.StringVar(&profileID, "profile-id", "", "profile id")
		fs.StringVar(&messageID, "message-id", "", "message id")
		fs.StringVar(&reactionID, "reaction-id", "", "reaction id")

		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(profileID) == "" {
			return errors.New("missing required flag --profile-id")
		}
		if strings.TrimSpace(messageID) == "" {
			return errors.New("missing required flag --message-id")
		}
		if strings.TrimSpace(reactionID) == "" {
			return errors.New("missing required flag --reaction-id")
		}

		client, err := newFeishuClient(app, profileID)
		if err != nil {
			return err
		}
		reaction, err := client.RemoveMessageReaction(ctx, messageID, reactionID)
		if err != nil {
			return decorateFeishuError(err)
		}
		return writeJSON(stdout, reaction)

	default:
		return fmt.Errorf("unknown feishu reaction subcommand %q", args[0])
	}
}

func runFeishuPinCommand(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: goclaw feishu pin <list|add|delete> [flags]")
	}

	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("feishu pin list", flag.ContinueOnError)
		fs.SetOutput(io.Discard)

		var profileID string
		var chatID string
		var pageSize int
		var pageToken string
		fs.StringVar(&profileID, "profile-id", "", "profile id")
		fs.StringVar(&chatID, "chat-id", "", "chat id")
		fs.IntVar(&pageSize, "page-size", 50, "page size")
		fs.StringVar(&pageToken, "page-token", "", "page token")

		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(profileID) == "" {
			return errors.New("missing required flag --profile-id")
		}
		if strings.TrimSpace(chatID) == "" {
			return errors.New("missing required flag --chat-id")
		}

		client, err := newFeishuClient(app, profileID)
		if err != nil {
			return err
		}
		page, err := client.ListPins(ctx, chatID, pageSize, pageToken)
		if err != nil {
			return decorateFeishuError(err)
		}
		return writeJSON(stdout, page)

	case "add":
		fs := flag.NewFlagSet("feishu pin add", flag.ContinueOnError)
		fs.SetOutput(io.Discard)

		var profileID string
		var messageID string
		fs.StringVar(&profileID, "profile-id", "", "profile id")
		fs.StringVar(&messageID, "message-id", "", "message id")

		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(profileID) == "" {
			return errors.New("missing required flag --profile-id")
		}
		if strings.TrimSpace(messageID) == "" {
			return errors.New("missing required flag --message-id")
		}

		client, err := newFeishuClient(app, profileID)
		if err != nil {
			return err
		}
		pin, err := client.PinMessage(ctx, messageID)
		if err != nil {
			return decorateFeishuError(err)
		}
		return writeJSON(stdout, pin)

	case "delete":
		fs := flag.NewFlagSet("feishu pin delete", flag.ContinueOnError)
		fs.SetOutput(io.Discard)

		var profileID string
		var messageID string
		fs.StringVar(&profileID, "profile-id", "", "profile id")
		fs.StringVar(&messageID, "message-id", "", "message id")

		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(profileID) == "" {
			return errors.New("missing required flag --profile-id")
		}
		if strings.TrimSpace(messageID) == "" {
			return errors.New("missing required flag --message-id")
		}

		client, err := newFeishuClient(app, profileID)
		if err != nil {
			return err
		}
		if err := client.UnpinMessage(ctx, messageID); err != nil {
			return decorateFeishuError(err)
		}
		return writeJSON(stdout, map[string]string{
			"profile_id": profileID,
			"message_id": messageID,
			"status":     "unpinned",
		})

	default:
		return fmt.Errorf("unknown feishu pin subcommand %q", args[0])
	}
}

func runFeishuScopesCommand(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: goclaw feishu scopes <list> [flags]")
	}

	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("feishu scopes list", flag.ContinueOnError)
		fs.SetOutput(io.Discard)

		var profileID string
		fs.StringVar(&profileID, "profile-id", "", "profile id")

		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(profileID) == "" {
			return errors.New("missing required flag --profile-id")
		}

		client, err := newFeishuClient(app, profileID)
		if err != nil {
			return err
		}
		scopes, err := client.ListAppScopes(ctx)
		if err != nil {
			return decorateFeishuError(err)
		}
		return writeJSON(stdout, scopes)

	default:
		return fmt.Errorf("unknown feishu scopes subcommand %q", args[0])
	}
}

func runFeishuCapabilitiesCommand(ctx context.Context, app *runtime.App, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: goclaw feishu capabilities <status> [flags]")
	}

	switch args[0] {
	case "status":
		fs := flag.NewFlagSet("feishu capabilities status", flag.ContinueOnError)
		fs.SetOutput(io.Discard)

		var profileID string
		fs.StringVar(&profileID, "profile-id", "", "profile id")

		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if strings.TrimSpace(profileID) == "" {
			return errors.New("missing required flag --profile-id")
		}

		account, ok := feishuchannel.ResolveAccountByProfile(
			app.Config.Channels.Feishu,
			domain.ProfileID(strings.TrimSpace(profileID)),
		)
		if !ok {
			return fmt.Errorf("feishu channel: profile %q not found", strings.TrimSpace(profileID))
		}

		client, err := newFeishuClient(app, profileID)
		if err != nil {
			return err
		}
		scopes, err := client.ListAppScopes(ctx)
		if err != nil {
			return decorateFeishuError(err)
		}
		report := feishuchannel.BuildCapabilityReport(account, scopes)
		return writeJSON(stdout, report)

	default:
		return fmt.Errorf("unknown feishu capabilities subcommand %q", args[0])
	}
}

func newFeishuClient(app *runtime.App, profileID string) (*rawfeishu.Client, error) {
	if app == nil {
		return nil, errors.New("missing app")
	}
	return feishuchannel.NewClientForProfile(app.Config.Channels.Feishu, domain.ProfileID(strings.TrimSpace(profileID)))
}

func splitCommaSeparated(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		result = append(result, part)
	}
	return result
}

func decorateFeishuError(err error) error {
	if permissionErr, ok := rawfeishu.ExtractPermissionError(err); ok {
		parts := []string{"feishu permission denied"}
		if len(permissionErr.MissingScopes) > 0 {
			parts = append(parts, "missing_scopes="+strings.Join(permissionErr.MissingScopes, ","))
		}
		if strings.TrimSpace(permissionErr.GrantURL) != "" {
			parts = append(parts, "grant_url="+strings.TrimSpace(permissionErr.GrantURL))
		}
		if strings.TrimSpace(permissionErr.Message) != "" {
			parts = append(parts, "message="+strings.TrimSpace(permissionErr.Message))
		}
		return errors.New(strings.Join(parts, " "))
	}
	return err
}
