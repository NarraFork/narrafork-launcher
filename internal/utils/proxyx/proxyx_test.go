package proxyx

import (
	"net/url"
	"strings"
	"testing"
)

func TestParseURL(t *testing.T) {
	ok := map[string]string{
		"127.0.0.1:7890":           "http://127.0.0.1:7890",
		"HTTP://proxy:3128/":       "http://proxy:3128",
		"https://p.example:443":    "https://p.example:443",
		"socks5://127.0.0.1:1080":  "socks5://127.0.0.1:1080",
		"socks5h://u:p@[::1]:1080": "socks5h://u:p@[::1]:1080",
	}
	for in, want := range ok {
		u, err := ParseURL(in)
		if err != nil || u.String() != want {
			t.Errorf("ParseURL(%q) = %v, %v; want %s", in, u, err, want)
		}
	}
	for _, bad := range []string{"", "ftp://h:1", "socks4://h:1", "http://:80", "http://h:0", "http://h:1/x", "http://h:1?q"} {
		if _, err := ParseURL(bad); err == nil {
			t.Errorf("ParseURL(%q) should fail", bad)
		}
	}
}

func TestFromEnvOrder(t *testing.T) {
	env := map[string]string{"http_proxy": "http://h:1", "ALL_PROXY": "socks5://a:2", "HTTPS_PROXY": "bogus://x", "no_proxy": "corp.example, .lan"}
	st := FromEnv(func(k string) string { return env[k] })
	if st.Origin != OriginEnv || st.HTTPS.String() != "http://h:1" {
		t.Fatalf("invalid HTTPS_PROXY should be skipped: %+v", st)
	}
	if len(st.Bypass) != 2 || st.Bypass[1] != ".lan" {
		t.Fatalf("bypass = %q", st.Bypass)
	}
	if FromEnv(func(string) string { return "" }).Enabled() {
		t.Fatal("empty env must be direct")
	}
}

func TestParseWinINet(t *testing.T) {
	if ParseWinINet(0, "127.0.0.1:7890", "").Enabled() {
		t.Fatal("ProxyEnable=0 must be direct")
	}
	st := ParseWinINet(1, "127.0.0.1:7890", ";localhost.*;;localhost;127.*;192.168.*;10.*;<local>")
	if !st.Enabled() || st.HTTP.String() != "http://127.0.0.1:7890" || st.HTTPS != st.HTTP || !st.BypassLocal {
		t.Fatalf("simple: %+v", st)
	}
	st = ParseWinINet(1, "http=h:1;https=s:2;socks=k:3", "")
	if st.HTTP.String() != "http://h:1" || st.HTTPS.String() != "http://s:2" {
		t.Fatalf("per-protocol: %+v", st)
	}
	st = ParseWinINet(1, "socks=k:3", "")
	if st.HTTP.String() != "socks5://k:3" || st.HTTPS.String() != "socks5://k:3" {
		t.Fatalf("socks only: %+v", st)
	}
	st = ParseWinINet(1, "ftp=f:1", "")
	if st.Enabled() {
		t.Fatalf("ftp-only must be direct: %+v", st)
	}
}

func TestBypassed(t *testing.T) {
	entries := []string{"*.corp.example", "192.168.*", "10.0.0.0/8", ".lan", "exact.example:8080", "172.16.*"}
	cases := map[string]bool{
		"localhost":         true,
		"app.localhost":     true,
		"127.0.0.1":         true,
		"127.8.9.1":         true,
		"::1":               true,
		"[::1]":             true,
		"git.corp.example":  true,
		"corp.example":      false,
		"192.168.1.5":       true,
		"10.2.3.4":          true,
		"nas.lan":           true,
		"lan":               true,
		"exact.example":     true,
		"sub.exact.example": true,
		"172.16.0.1":        true,
		"172.17.0.1":        false,
		"github.com":        false,
		"api.github.com":    false,
		"intranet":          false,
	}
	for host, want := range cases {
		if got := Bypassed(host, entries, false); got != want {
			t.Errorf("Bypassed(%q) = %v, want %v", host, got, want)
		}
	}
	if !Bypassed("intranet", nil, true) || Bypassed("github.com", nil, true) {
		t.Error("<local> must bypass dotless hosts only")
	}
	if !Bypassed("anything.example", []string{"*"}, false) {
		t.Error("* must bypass everything")
	}
}

func TestProxyFor(t *testing.T) {
	st := ParseWinINet(1, "http=h:1;https=s:2", "<local>")
	mustURL := func(s string) *url.URL { u, _ := url.Parse(s); return u }
	if got := st.ProxyFor(mustURL("https://github.com/x")); got == nil || got.Host != "s:2" {
		t.Fatalf("https → %v", got)
	}
	if got := st.ProxyFor(mustURL("http://example.com/")); got == nil || got.Host != "h:1" {
		t.Fatalf("http → %v", got)
	}
	for _, u := range []string{"http://localhost:7778/", "http://[::1]:7778/", "http://127.0.0.1/", "http://nas/"} {
		if got := st.ProxyFor(mustURL(u)); got != nil {
			t.Errorf("%s must be direct, got %v", u, got)
		}
	}
	if (Settings{}).ProxyFor(mustURL("https://github.com")) != nil {
		t.Fatal("zero settings must be direct")
	}
}

func TestNoProxyListAndEnvVars(t *testing.T) {
	st := ParseWinINet(1, "127.0.0.1:7890", ";localhost.*;;localhost;127.*;192.168.*;10.*;172.16.*;*.corp.example;foo*bar;<local>")
	got := NoProxyList(st)
	want := "localhost,127.0.0.1,::1,127.0.0.0/8,192.168.0.0/16,10.0.0.0/8,172.16.0.0/16,.corp.example"
	if got != want {
		t.Fatalf("NoProxyList = %s\nwant          %s", got, want)
	}
	vars := EnvVars(st)
	for _, k := range EnvKeys {
		if vars[k] == "" {
			t.Errorf("%s not set", k)
		}
	}
	if vars["HTTPS_PROXY"] != "http://127.0.0.1:7890" || vars["no_proxy"] != want {
		t.Fatalf("vars = %v", vars)
	}
	if EnvVars(Settings{}) != nil {
		t.Fatal("disabled settings must produce no vars")
	}
	if strings.Contains(Redact(mustParse(t, "http://u:secret@h:1")), "secret") {
		t.Fatal("Redact must hide the password")
	}
}

func mustParse(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := ParseURL(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
