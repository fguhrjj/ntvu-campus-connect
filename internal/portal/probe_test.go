package portal

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"campusconnect/internal/netguard"
)

func TestInternetProbeRequiresExactBody(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   bool
	}{
		{200, "Microsoft Connect Test", true},
		{200, "<html>请登录校园网</html>", false},
		{302, "", false},
		{200, "Microsoft Connect Test and more", false},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if tc.status == 302 {
				w.Header().Set("Location", "http://123.123.123.123/")
			}
			w.WriteHeader(tc.status)
			io.WriteString(w, tc.body)
		}))
		p := InternetProbe{HTTP: netguard.NewClient(allowAll{}, srv.Client().Transport, 7*time.Second), URL: srv.URL + "/connecttest.txt"}
		if got := p.Internet(context.Background()); got != tc.want {
			t.Errorf("status %d body %q: Internet = %v, want %v", tc.status, tc.body, got, tc.want)
		}
		srv.Close()
	}
}
