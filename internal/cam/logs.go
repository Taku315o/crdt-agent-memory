package cam

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

type LogOptions struct {
	Service string
	Tail    int
	Follow  bool
}

func (a *App) Logs(ctx context.Context, w io.Writer, opts LogOptions) error {
	layout, err := ResolveLayout(a.Profile)
	if err != nil {
		return err
	}
	state, err := LoadRuntime(layout.RuntimePath)
	if err != nil {
		return err
	}
	if opts.Service == "" && opts.Follow {
		return fmt.Errorf("--follow requires --service")
	}

	services := make([]RuntimeService, 0, 3)
	if opts.Service != "" {
		logPath := layout.logPath(opts.Service)
		services = append(services, RuntimeService{Name: opts.Service, LogPath: logPath})
	} else if state != nil {
		services = append(services, state.Services...)
	} else {
		for _, name := range []string{"memoryd", "indexd", "syncd"} {
			logPath := layout.logPath(name)
			if _, err := os.Stat(logPath); err == nil {
				services = append(services, RuntimeService{Name: name, LogPath: logPath})
			}
		}
	}
	if len(services) == 0 {
		return fmt.Errorf("no logs found for profile %q", a.Profile)
	}
	for i, svc := range services {
		if opts.Service == "" && len(services) > 1 {
			if i > 0 {
				fmt.Fprintln(w)
			}
			fmt.Fprintf(w, "== %s ==\n", svc.Name)
		}
		if err := printTail(w, svc.LogPath, opts.Tail); err != nil {
			return err
		}
		if opts.Follow {
			return followLog(ctx, w, svc.LogPath)
		}
	}
	return nil
}

func printTail(w io.Writer, path string, tail int) error {
	lines, err := readLines(path)
	if err != nil {
		return err
	}
	if tail <= 0 || tail > len(lines) {
		tail = len(lines)
	}
	start := len(lines) - tail
	for _, line := range lines[start:] {
		fmt.Fprintln(w, line)
	}
	return nil
}

func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

func followLog(ctx context.Context, w io.Writer, path string) error {
	offset := int64(0)
	if info, err := os.Stat(path); err == nil {
		offset = info.Size()
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if info.Size() < offset {
			offset = 0
		}
		if info.Size() == offset {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			_ = f.Close()
			return err
		}
		reader := bufio.NewReader(f)
		for {
			line, err := reader.ReadString('\n')
			if line != "" {
				fmt.Fprint(w, strings.TrimRight(line, "\n")+"\n")
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				_ = f.Close()
				return err
			}
		}
		offset = info.Size()
		_ = f.Close()
	}
}
