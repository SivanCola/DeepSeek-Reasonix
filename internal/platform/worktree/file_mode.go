package worktree

import (
	"context"
	"fmt"
	"strings"

	"reasonix/internal/platform/gitcmd"
)

func indexModesWhenUntrusted(ctx context.Context, repo gitcmd.Repo, paths []string) (map[string]string, error) {
	out, stderr, err := runGit(ctx, repo, "config", "--bool", "--default=true", "--get", "core.filemode")
	if err != nil {
		return nil, fmt.Errorf("read filemode policy: %w%s", err, stderrSuffix(stderr))
	}
	if strings.TrimSpace(out) == "true" {
		return nil, nil
	}
	args := append([]string{"ls-files", "--stage", "-z", "--"}, paths...)
	out, stderr, err = runGit(ctx, repo, args...)
	if err != nil {
		return nil, fmt.Errorf("read index modes: %w%s", err, stderrSuffix(stderr))
	}
	modes := map[string]string{}
	for record := range strings.SplitSeq(strings.TrimSuffix(out, "\x00"), "\x00") {
		meta, path, ok := strings.Cut(record, "\t")
		fields := strings.Fields(meta)
		if ok && len(fields) == 3 && fields[2] == "0" {
			modes[path] = fields[0]
		}
	}
	return modes, nil
}
