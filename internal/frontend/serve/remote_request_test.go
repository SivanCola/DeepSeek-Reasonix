package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"reasonix/internal/contract/config"
)

func saveTestRemoteHosts(t *testing.T, names ...string) {
	t.Helper()
	if err := config.EditUserConfigWithCredentials(func(c *config.Config) ([]config.CredentialChange, error) {
		for _, name := range names {
			if err := c.UpsertRemoteHost(config.RemoteHostEntry{Name: name, Host: "example.invalid"}); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteRequestsRefuseUnknownHostsBeforeDial(t *testing.T) {
	for _, host := range []string{"missing", "attacker@evil.example:22"} {
		for _, route := range []string{"sessions/pin", "sessions/archive", "sessions/viewed", "sessions/remove", "tree", "dirs", "probe", "open"} {
			t.Run(host+"/"+route, func(t *testing.T) {
				calls := 0
				far := remoteWorkspaceKernel(t)
				front := bookServer(t, &stubAttacher{
					attach: func(host, _ string) (RemoteEndpoint, func(), error) {
						calls++
						return RemoteEndpoint{Host: host, Workspace: "/srv/test", Addr: far.Listener.Addr().String(), Token: "test"}, func() {}, nil
					},
					browse: func(_, _ string) (RemoteListing, error) { calls++; return RemoteListing{}, nil },
				})
				path := "/remotes/" + url.PathEscape(host) + "/" + route
				var resp *http.Response
				if route == "open" {
					body, _ := json.Marshal(OpenRemoteRequest{Host: host, Workspace: "/srv/explicit"})
					resp = bookPost(t, front, "/remotes/open", string(body))
				} else if strings.HasPrefix(route, "sessions/") {
					resp = bookPost(t, front, path, `{"path":"/same/session.jsonl","pinned":true,"archived":true}`)
				} else {
					var err error
					resp, err = http.Get(front.URL + path)
					if err != nil {
						t.Fatal(err)
					}
				}
				defer resp.Body.Close()
				var reason Reason
				_ = json.NewDecoder(resp.Body).Decode(&reason)
				if resp.StatusCode != http.StatusNotFound || reason.Code != "remote.unknown_host" || calls != 0 {
					t.Fatalf("status=%d reason=%+v remote calls=%d", resp.StatusCode, reason, calls)
				}
			})
		}
	}
}

func TestRemoteSessionMutationsBoundEntireBodyBeforeDial(t *testing.T) {
	for _, action := range []string{"pin", "archive", "viewed", "remove"} {
		for _, payload := range []string{
			fmt.Sprintf(`{"path":%q}`, strings.Repeat("x", 64<<10)),
			`{"path":"/same/session.jsonl"}` + strings.Repeat(" ", 64<<10),
			`{"path":"/same/session.jsonl"} {}`,
		} {
			t.Run(action+fmt.Sprint(len(payload)), func(t *testing.T) {
				calls := 0
				far := remoteWorkspaceKernel(t)
				front := bookServer(t, &stubAttacher{attach: func(host, _ string) (RemoteEndpoint, func(), error) {
					calls++
					return RemoteEndpoint{Host: host, Addr: far.Listener.Addr().String(), Token: "test"}, func() {}, nil
				}})
				bookPost(t, front, "/remotes", `{"name":"gpu","host":"example.invalid"}`)
				resp := bookPost(t, front, "/remotes/gpu/sessions/"+action, payload)
				var reason Reason
				_ = json.NewDecoder(resp.Body).Decode(&reason)
				if resp.StatusCode != http.StatusBadRequest || reason.Code != codeBadBody || calls != 0 {
					t.Fatalf("status=%d reason=%+v remote calls=%d", resp.StatusCode, reason, calls)
				}
			})
		}
	}
}
