package feishuchannel

import (
	"fmt"
	"strings"

	"goclaw/internal/config"
	"goclaw/internal/domain"
	rawfeishu "goclaw/internal/feishu"
)

type Account struct {
	AccountID      string
	Name           string
	ProfileID      domain.ProfileID
	Enabled        bool
	Configured     bool
	ConnectionMode string
	Actions        config.FeishuActionsConfig
	APIBaseURL     string
	AppID          string
	AppSecret      string
}

func ResolveAccountByProfile(cfg config.FeishuConfig, profileID domain.ProfileID) (Account, bool) {
	trimmedProfileID := domain.ProfileID(strings.TrimSpace(string(profileID)))
	for _, account := range resolveAccounts(cfg) {
		if !account.Enabled || account.Settings.ProfileID != trimmedProfileID {
			continue
		}
		return Account{
			AccountID:      account.AccountID,
			Name:           account.Settings.Name,
			ProfileID:      account.Settings.ProfileID,
			Enabled:        account.Enabled,
			Configured:     account.Configured,
			ConnectionMode: account.Settings.ConnectionMode,
			Actions:        account.Settings.Actions,
			APIBaseURL:     account.Settings.APIBaseURL,
			AppID:          account.Settings.AppID,
			AppSecret:      account.Settings.AppSecret,
		}, true
	}
	return Account{}, false
}

func NewClientForProfile(
	cfg config.FeishuConfig,
	profileID domain.ProfileID,
) (*rawfeishu.Client, error) {
	account, ok := ResolveAccountByProfile(cfg, profileID)
	if !ok {
		return nil, fmt.Errorf("feishu channel: profile %q not found", profileID)
	}
	if !account.Configured {
		return nil, fmt.Errorf("feishu channel: profile %q is not fully configured", profileID)
	}
	return rawfeishu.NewClient(rawfeishu.ClientConfig{
		BaseURL:   account.APIBaseURL,
		AppID:     account.AppID,
		AppSecret: account.AppSecret,
	}), nil
}
