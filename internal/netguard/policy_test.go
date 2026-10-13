package netguard

import (
	"net/http"
	"net/url"
	"testing"
)

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}

func TestPolicyAllowsSpecRequests(t *testing.T) {
	p := ProductionPolicy()
	allowed := []struct{ method, raw string }{
		{http.MethodGet, "https://nap.ntvu.edu.cn/"},
		{http.MethodGet, "https://nap.ntvu.edu.cn/eportal/redirect.jsp"},
		{http.MethodGet, "http://123.123.123.123/"},
		{http.MethodGet, "https://nap.ntvu.edu.cn/eportal/index.jsp?wlanuserip=x&mac=y"},
		{http.MethodPost, "https://nap.ntvu.edu.cn/eportal/network/userOnline"},
		{http.MethodGet, "https://nap.ntvu.edu.cn/eportal/adaptor/getOnlineUserInfo?sessionId=x"},
		{http.MethodPost, "https://nap.ntvu.edu.cn/eportal/workFlow/getCurrentNode"},
		{http.MethodPost, "https://nap.ntvu.edu.cn/eportal/network/serviceSelection"},
		{http.MethodPost, "https://nap.ntvu.edu.cn/eportal/network/serviceLogin"},
		{http.MethodGet, "https://nap.ntvu.edu.cn/cas-sso/login?flowSessionId=x"},
		{http.MethodGet, "https://nap.ntvu.edu.cn/cas-sso/clientredirect?client_name=adapter&service=x"},
		{http.MethodGet, "https://authserver.ntvu.edu.cn/authserver/login?service=x"},
		{http.MethodPost, "https://authserver.ntvu.edu.cn/authserver/login?service=x"},
		{http.MethodGet, "https://authserver.ntvu.edu.cn/authserver/checkNeedCaptcha.htl?username=x"},
		{http.MethodGet, "http://www.msftconnecttest.com/connecttest.txt"},
	}
	for _, c := range allowed {
		if !p.Allow(c.method, mustURL(t, c.raw)) {
			t.Errorf("Allow(%s %s) = false, want true", c.method, c.raw)
		}
	}
}

func TestPolicyRejectsHostSuffixSpoof(t *testing.T) {
	p := ProductionPolicy()
	for _, raw := range []string{
		"https://nap.ntvu.edu.cn.evil.example/eportal/network/userOnline",
		"https://evilnap.ntvu.edu.cn/eportal/network/userOnline",
		"https://nap.ntvu.edu.cn:8443/eportal/network/userOnline",
		"https://authserver.ntvu.edu.cn.evil.example/authserver/login",
		"https://user@evil.example/authserver/login",
	} {
		if p.Allow(http.MethodPost, mustURL(t, raw)) {
			t.Errorf("Allow(POST %s) = true, want false", raw)
		}
	}
}

func TestPolicyRejectsUnlistedPathOrScheme(t *testing.T) {
	p := ProductionPolicy()
	rejected := []struct{ method, raw string }{
		// Logout / offline endpoints found in the portal bundles.
		{http.MethodPost, "https://nap.ntvu.edu.cn/eportal/network/offline"},
		{http.MethodGet, "https://nap.ntvu.edu.cn/eportal/auth/json/logout"},
		{http.MethodGet, "https://nap.ntvu.edu.cn/portal/portalAuthen/logout"},
		{http.MethodGet, "https://nap.ntvu.edu.cn/portal/cas/getLogout"},
		{http.MethodGet, "https://nap.ntvu.edu.cn/cas-sso/logout?service=x"},
		{http.MethodGet, "https://authserver.ntvu.edu.cn/authserver/logout?service=x"},
		{http.MethodPost, "https://nap.ntvu.edu.cn/eportal/adaptor/kick-offline/batch"},
		{http.MethodPost, "https://nap.ntvu.edu.cn/eportal/workFlow/updateUserSession"},
		// Not visiting the portal SPA itself.
		{http.MethodGet, "http://nap.ntvu.edu.cn/portal/portal-main?sessionId=x"},
		// Credentials and portal APIs never over plain HTTP.
		{http.MethodPost, "http://authserver.ntvu.edu.cn/authserver/login"},
		{http.MethodPost, "http://nap.ntvu.edu.cn/eportal/network/serviceLogin"},
		// Path tricks.
		{http.MethodGet, "https://nap.ntvu.edu.cn/cas-sso/login/../logout"},
		{http.MethodPost, "https://nap.ntvu.edu.cn/eportal/network/userOnline/"},
		// Wrong method.
		{http.MethodPost, "https://nap.ntvu.edu.cn/cas-sso/login"},
		{http.MethodPost, "https://nap.ntvu.edu.cn/eportal/adaptor/getOnlineUserInfo"},
		{http.MethodDelete, "https://authserver.ntvu.edu.cn/authserver/login"},
		// clientredirect only for the adapter client.
		{http.MethodGet, "https://nap.ntvu.edu.cn/cas-sso/clientredirect?client_name=sidadapter"},
		{http.MethodGet, "https://nap.ntvu.edu.cn/cas-sso/clientredirect?client_name=adapter&client_name=Wechat"},
		// Probe only over its fixed URL.
		{http.MethodGet, "https://www.msftconnecttest.com/redirect"},
		// NAS intercept only as its bare origin, and its index.jsp only over https.
		{http.MethodGet, "http://123.123.123.123/?url=x"},
		{http.MethodGet, "http://123.123.123.123/login"},
		{http.MethodGet, "https://123.123.123.123/"},
		{http.MethodPost, "http://123.123.123.123/"},
		{http.MethodGet, "http://nap.ntvu.edu.cn/eportal/index.jsp?wlanuserip=x"},
	}
	for _, c := range rejected {
		if p.Allow(c.method, mustURL(t, c.raw)) {
			t.Errorf("Allow(%s %s) = true, want false", c.method, c.raw)
		}
	}
}
