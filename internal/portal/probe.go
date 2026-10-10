package portal

import (
	"context"
	"io"
	"net/http"

	"campusconnect/internal/netguard"
)

// ProbeURL is the connectivity check from spec §4.
const ProbeURL = "http://www.msftconnecttest.com/connecttest.txt"

const probeBody = "Microsoft Connect Test"

// InternetProbe checks real internet reachability behind the portal.
type InternetProbe struct {
	HTTP *netguard.Client
	URL  string
}

// Internet is true only for a 200 whose body is exactly the expected text;
// a captive-portal redirect or page counts as no internet.
func (p InternetProbe) Internet(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.URL, nil)
	if err != nil {
		return false
	}
	resp, err := p.HTTP.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, int64(len(probeBody)+1)))
	return err == nil && string(b) == probeBody
}
