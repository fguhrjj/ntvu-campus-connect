package portal

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// jsonAPI serves one canned body per path and records request bodies.
type jsonAPI struct {
	t       *testing.T
	method  map[string]string
	replies map[string]string
	bodies  map[string]map[string]any
	queries map[string]string
}

func newJSONAPI(t *testing.T) *jsonAPI {
	return &jsonAPI{t: t, method: map[string]string{}, replies: map[string]string{}, bodies: map[string]map[string]any{}, queries: map[string]string{}}
}

func (a *jsonAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	reply, ok := a.replies[r.URL.Path]
	if !ok {
		a.t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
		return
	}
	a.method[r.URL.Path] = r.Method
	a.queries[r.URL.Path] = r.URL.RawQuery
	if r.Method == http.MethodPost {
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			a.t.Errorf("%s Content-Type = %q", r.URL.Path, ct)
		}
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			a.t.Errorf("%s body not JSON: %q", r.URL.Path, b)
		}
		a.bodies[r.URL.Path] = m
	}
	w.Header().Set("Content-Type", "application/json;charset=UTF-8")
	io.WriteString(w, reply)
}

func TestUserOnlineReadsOnlineFlag(t *testing.T) {
	for _, tc := range []struct {
		reply string
		want  bool
	}{
		{`{"code":200,"message":null,"data":{"sessionId":"sid","online":true,"userIndex":"x","message":"redirect success"}}`, true},
		{`{"code":200,"message":null,"data":{"sessionId":"sid","online":false,"userIndex":"","portalSuccessUrl":null}}`, false},
	} {
		api := newJSONAPI(t)
		api.replies["/eportal/network/userOnline"] = tc.reply
		c, _, _ := newTestClient(t, api)
		got, err := c.UserOnline(context.Background(), "sid")
		if err != nil {
			t.Fatalf("UserOnline: %v", err)
		}
		if got != tc.want {
			t.Errorf("online = %v, want %v", got, tc.want)
		}
		if api.method["/eportal/network/userOnline"] != http.MethodPost || api.bodies["/eportal/network/userOnline"]["sessionId"] != "sid" {
			t.Errorf("request = %s %v", api.method["/eportal/network/userOnline"], api.bodies["/eportal/network/userOnline"])
		}
	}
}

func TestAPINon200CodeIsUnavailable(t *testing.T) {
	api := newJSONAPI(t)
	api.replies["/eportal/network/userOnline"] = `{"code":500,"message":"会话不存在","data":null}`
	c, _, _ := newTestClient(t, api)
	_, err := c.UserOnline(context.Background(), "sid")
	var pe *Error
	if !errors.As(err, &pe) || pe.Kind != KindUnavailable || !strings.Contains(pe.Message, "会话不存在") {
		t.Fatalf("err = %v, want KindUnavailable with server message", err)
	}
}

func TestAPIMalformedJSONIsChanged(t *testing.T) {
	api := newJSONAPI(t)
	api.replies["/eportal/network/userOnline"] = `<html>maintenance</html>`
	c, _, _ := newTestClient(t, api)
	_, err := c.UserOnline(context.Background(), "sid")
	var pe *Error
	if !errors.As(err, &pe) || pe.Kind != KindChanged {
		t.Fatalf("err = %v, want KindChanged", err)
	}
}

func TestCurrentNodeSendsFlowKey(t *testing.T) {
	api := newJSONAPI(t)
	api.replies["/eportal/workFlow/getCurrentNode"] = `{"code":200,"message":null,"data":{"currentNodeId":"n","sessionId":"sid","flowKey":"portal_auth","currentNodePath":"authenticate","flowSessionInfo":null,"userinfo":{}}}`
	c, _, _ := newTestClient(t, api)
	n, err := c.CurrentNode(context.Background(), "sid", "portal_auth")
	if err != nil {
		t.Fatalf("CurrentNode: %v", err)
	}
	if n.Path != "authenticate" {
		t.Errorf("path = %q", n.Path)
	}
	b := api.bodies["/eportal/workFlow/getCurrentNode"]
	if b["sessionId"] != "sid" || b["flowKey"] != "portal_auth" {
		t.Errorf("body = %v", b)
	}
}

func TestCurrentNodeEmptyPathIsChanged(t *testing.T) {
	api := newJSONAPI(t)
	api.replies["/eportal/workFlow/getCurrentNode"] = `{"code":200,"data":{"currentNodePath":""}}`
	c, _, _ := newTestClient(t, api)
	_, err := c.CurrentNode(context.Background(), "sid", "portal_auth")
	var pe *Error
	if !errors.As(err, &pe) || pe.Kind != KindChanged {
		t.Fatalf("err = %v, want KindChanged", err)
	}
}

func TestServicesMapsKeyToLabelAndValue(t *testing.T) {
	api := newJSONAPI(t)
	api.replies["/eportal/network/serviceSelection"] = `{"code":200,"message":null,"data":[{"key":"校园网","value":"PUBLIC"},{"key":"中国联通","value":"CUCC"}]}`
	c, _, _ := newTestClient(t, api)
	got, err := c.Services(context.Background(), "sid")
	if err != nil {
		t.Fatalf("Services: %v", err)
	}
	want := []Service{{Label: "校园网", Value: "PUBLIC"}, {Label: "中国联通", Value: "CUCC"}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("services = %+v, want %+v", got, want)
	}
	if api.bodies["/eportal/network/serviceSelection"]["sessionId"] != "sid" {
		t.Errorf("body = %v", api.bodies["/eportal/network/serviceSelection"])
	}
}

func TestServiceLoginRequiresAuthResultSuccess(t *testing.T) {
	for _, tc := range []struct {
		name, reply string
		ok          bool
		msg         string
	}{
		{"success", `{"code":200,"message":null,"data":{"authResult":"success","authMessage":""}}`, true, ""},
		{"refused", `{"code":200,"message":null,"data":{"authResult":"fail","authMessage":"运营商账号欠费"}}`, false, "运营商账号欠费"},
		{"missing result", `{"code":200,"message":null,"data":{}}`, false, ""},
		{"code error", `{"code":500,"message":"服务不可用","data":null}`, false, "服务不可用"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := newJSONAPI(t)
			api.replies["/eportal/network/serviceLogin"] = tc.reply
			c, _, _ := newTestClient(t, api)
			err := c.ServiceLogin(context.Background(), "sid", "CUCC")
			if tc.ok {
				if err != nil {
					t.Fatalf("ServiceLogin: %v", err)
				}
			} else {
				var pe *Error
				if !errors.As(err, &pe) || pe.Kind != KindRejected || !strings.Contains(pe.Message, tc.msg) {
					t.Fatalf("err = %v, want KindRejected containing %q", err, tc.msg)
				}
			}
			b := api.bodies["/eportal/network/serviceLogin"]
			if b["sessionId"] != "sid" || b["service"] != "CUCC" {
				t.Errorf("body = %v", b)
			}
		})
	}
}

func TestOnlineUserInfoReadsPortalOnlineUserInfo(t *testing.T) {
	api := newJSONAPI(t)
	api.replies["/eportal/adaptor/getOnlineUserInfo"] = `{"code":200,"message":null,"data":{"portalOnlineUserInfo":{"result":"success","userName":"20230001","userId":"20230001","service":"CUCC","ssid":"Ruijie","loginType":"2"},"onlineUser":{"userId":"20230001","service":"CUCC"}}}`
	c, _, _ := newTestClient(t, api)
	info, online, err := c.OnlineUserInfo(context.Background(), "sid")
	if err != nil {
		t.Fatalf("OnlineUserInfo: %v", err)
	}
	if !online || info != (OnlineInfo{UserID: "20230001", Service: "CUCC"}) {
		t.Fatalf("info = %+v online = %v", info, online)
	}
	if api.method["/eportal/adaptor/getOnlineUserInfo"] != http.MethodGet || api.queries["/eportal/adaptor/getOnlineUserInfo"] != "sessionId=sid" {
		t.Errorf("request = %s ?%s", api.method["/eportal/adaptor/getOnlineUserInfo"], api.queries["/eportal/adaptor/getOnlineUserInfo"])
	}
}

func TestOnlineUserInfoWithoutOnlineUserIsOffline(t *testing.T) {
	for _, data := range []string{
		`{"portalOnlineUserInfo":null,"onlineUser":null}`,
		`{"portalOnlineUserInfo":{"result":"fail"},"onlineUser":{}}`,
	} {
		api := newJSONAPI(t)
		api.replies["/eportal/adaptor/getOnlineUserInfo"] = `{"code":200,"data":` + data + `}`
		c, _, _ := newTestClient(t, api)
		_, online, err := c.OnlineUserInfo(context.Background(), "sid")
		if err != nil {
			t.Fatalf("OnlineUserInfo: %v", err)
		}
		if online {
			t.Errorf("data %s reported online", data)
		}
	}
}
