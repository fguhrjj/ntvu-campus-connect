package portal

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
)

type sessionBody struct {
	SessionID string `json:"sessionId"`
}

// UserOnline reports data.online from POST /eportal/network/userOnline.
func (c *Client) UserOnline(ctx context.Context, sessionID string) (bool, error) {
	var data struct {
		Online bool `json:"online"`
	}
	err := c.call(ctx, "userOnline", "/eportal/network/userOnline", nil, sessionBody{sessionID}, &data)
	return data.Online, err
}

// OnlineUserInfo reads GET /eportal/adaptor/getOnlineUserInfo. online is true
// when data.onlineUser is a non-empty object (the finish page's check).
func (c *Client) OnlineUserInfo(ctx context.Context, sessionID string) (OnlineInfo, bool, error) {
	var data struct {
		Portal *struct {
			Result  string `json:"result"`
			UserID  string `json:"userId"`
			Service string `json:"service"`
		} `json:"portalOnlineUserInfo"`
		OnlineUser map[string]json.RawMessage `json:"onlineUser"`
	}
	err := c.call(ctx, "getOnlineUserInfo", "/eportal/adaptor/getOnlineUserInfo", url.Values{"sessionId": {sessionID}}, nil, &data)
	if err != nil {
		return OnlineInfo{}, false, err
	}
	var info OnlineInfo
	if data.Portal != nil && data.Portal.Result == "success" {
		info = OnlineInfo{UserID: data.Portal.UserID, Service: data.Portal.Service}
	}
	return info, len(data.OnlineUser) > 0, nil
}

// CurrentNode reads the workflow position from POST /eportal/workFlow/getCurrentNode.
func (c *Client) CurrentNode(ctx context.Context, sessionID, flowKey string) (Node, error) {
	body := struct {
		SessionID string `json:"sessionId"`
		FlowKey   string `json:"flowKey"`
	}{sessionID, flowKey}
	var data struct {
		Path string `json:"currentNodePath"`
	}
	if err := c.call(ctx, "getCurrentNode", "/eportal/workFlow/getCurrentNode", nil, body, &data); err != nil {
		return Node{}, err
	}
	if data.Path == "" {
		return Node{}, &Error{Kind: KindChanged, Op: "getCurrentNode", Message: "empty currentNodePath"}
	}
	return Node{Path: data.Path}, nil
}

// Services lists operator choices from POST /eportal/network/serviceSelection.
func (c *Client) Services(ctx context.Context, sessionID string) ([]Service, error) {
	var data []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := c.call(ctx, "serviceSelection", "/eportal/network/serviceSelection", nil, sessionBody{sessionID}, &data); err != nil {
		return nil, err
	}
	out := make([]Service, 0, len(data))
	for _, d := range data {
		out = append(out, Service{Label: d.Key, Value: d.Value})
	}
	return out, nil
}

// ServiceLogin submits the operator choice. Only code 200 with
// data.authResult == "success" counts as accepted (spec §2.3).
func (c *Client) ServiceLogin(ctx context.Context, sessionID, service string) error {
	const op = "serviceLogin"
	body := struct {
		SessionID string `json:"sessionId"`
		Service   string `json:"service"`
	}{sessionID, service}
	var data struct {
		AuthResult  string `json:"authResult"`
		AuthMessage string `json:"authMessage"`
	}
	if err := c.call(ctx, op, "/eportal/network/serviceLogin", nil, body, &data); err != nil {
		var pe *Error
		if errors.As(err, &pe) && pe.Code != 0 {
			// The portal answered with code != 200: a refusal, not an outage.
			pe.Kind = KindRejected
		}
		return err
	}
	if data.AuthResult != "success" {
		return &Error{Kind: KindRejected, Op: op, Message: data.AuthMessage}
	}
	return nil
}
