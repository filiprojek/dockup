package inventory

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/compose-spec/compose-go/v2/cli"
	"github.com/compose-spec/compose-go/v2/types"

	"github.com/filiprojek/dockup/internal/ports"
)

// findComposeFiles returns the compose file (plus override, if any) directly
// inside dir, using compose's own precedence. Nil if none.
func findComposeFiles(dir string) []string {
	pick := func(names []string) string {
		for _, n := range names {
			p := filepath.Join(dir, n)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p
			}
		}
		return ""
	}
	main := pick(cli.DefaultFileNames)
	if main == "" {
		return nil
	}
	files := []string{main}
	if o := pick(cli.DefaultOverrideFileNames); o != "" {
		files = append(files, o)
	}
	return files
}

// loadCompose parses a project the way `docker compose` would: .env in the
// project dir, OS environment, all profiles visible.
func loadCompose(ctx context.Context, dir string, files []string, name string) (*types.Project, error) {
	opts := []cli.ProjectOptionsFn{
		cli.WithWorkingDirectory(dir),
		cli.WithOsEnv,
		cli.WithEnvFiles(),
		cli.WithDotEnv,
		cli.WithProfiles([]string{"*"}),
	}
	if name != "" {
		opts = append(opts, cli.WithName(name))
	}
	o, err := cli.NewProjectOptions(files, opts...)
	if err != nil {
		return nil, err
	}
	return o.LoadProject(ctx)
}

// declaredPorts converts compose port mappings to host-side bindings.
// Unpublished ("8080") and random-host-port mappings are skipped.
func declaredPorts(project, service string, cfg []types.ServicePortConfig) []ports.Declared {
	var out []ports.Declared
	for _, p := range cfg {
		if p.Published == "" {
			continue
		}
		ip := netip.IPv4Unspecified()
		if p.HostIP != "" {
			a, err := netip.ParseAddr(p.HostIP)
			if err != nil {
				continue
			}
			ip = a.Unmap()
		}
		proto := ports.TCP
		if strings.EqualFold(p.Protocol, "udp") {
			proto = ports.UDP
		}
		nums, err := ports.ParseSpec(p.Published)
		if err != nil {
			continue
		}
		for _, n := range nums {
			out = append(out, ports.Declared{Project: project, Service: service, Proto: proto, IP: ip, Port: n})
		}
	}
	return out
}
