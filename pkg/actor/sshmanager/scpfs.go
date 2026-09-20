package sshmanager

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"github.com/qomos-w/sporemind/pkg/domain"
	"golang.org/x/crypto/ssh"
)

// remoteFS abstracts file operations over either SFTP or SCP/exec.
type remoteFS interface {
	List(dir string) ([]domain.SshFileEntry, error)
	Read(p string) ([]byte, error)
	Write(p string, content []byte) error
	Mkdir(p string) error
	Remove(p string) error
	RemoveDir(p string) error
	Rename(from, to string) error
	Chmod(p string, mode os.FileMode) error
	Stat(p string) (name string, size int64, isDir bool, mode string, modTime time.Time, err error)
	Lstat(p string) (name string, size int64, isDir bool, mode string, modTime time.Time, err error)
	Close() error
}

// ---- SFTP backend (wraps *sftp.Client) ----

type sftpBackend struct{ raw *sftp.Client }

func (b *sftpBackend) List(dir string) ([]domain.SshFileEntry, error) {
	infos, err := b.raw.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	entries := make([]domain.SshFileEntry, 0, len(infos))
	for _, info := range infos {
		entries = append(entries, domain.SshFileEntry{
			Name:     info.Name(),
			FullPath: path.Join(dir, info.Name()),
			IsDir:    info.IsDir(),
			Size:     info.Size(),
			Mode:     info.Mode().String(),
			Modified: info.ModTime().Format(time.RFC3339),
		})
	}
	return entries, nil
}

func (b *sftpBackend) Read(p string) ([]byte, error) {
	f, err := b.raw.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

func (b *sftpBackend) Write(p string, content []byte) error {
	f, err := b.raw.Create(p)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(content)
	return err
}

func (b *sftpBackend) Mkdir(p string) error { return b.raw.MkdirAll(p) }

func (b *sftpBackend) Remove(p string) error { return b.raw.Remove(p) }

func (b *sftpBackend) RemoveDir(p string) error {
	return removeDirRecursive(b.raw, p)
}

func (b *sftpBackend) Rename(from, to string) error { return b.raw.Rename(from, to) }

func (b *sftpBackend) Chmod(p string, mode os.FileMode) error { return b.raw.Chmod(p, mode) }

func (b *sftpBackend) Stat(p string) (string, int64, bool, string, time.Time, error) {
	info, err := b.raw.Stat(p)
	if err != nil {
		return "", 0, false, "", time.Time{}, err
	}
	return info.Name(), info.Size(), info.IsDir(), info.Mode().String(), info.ModTime(), nil
}

func (b *sftpBackend) Lstat(p string) (string, int64, bool, string, time.Time, error) {
	info, err := b.raw.Lstat(p)
	if err != nil {
		return "", 0, false, "", time.Time{}, err
	}
	return info.Name(), info.Size(), info.IsDir(), info.Mode().String(), info.ModTime(), nil
}

func (b *sftpBackend) Close() error { return b.raw.Close() }

// ---- SCP/exec backend (uses ssh exec session) ----

type execBackend struct{ sshClient *ssh.Client }

func (b *execBackend) run(cmd string) (stdout string, err error) {
	sess, err := b.sshClient.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	var buf bytes.Buffer
	sess.Stdout = &buf
	if err := sess.Run(cmd); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (b *execBackend) List(dir string) ([]domain.SshFileEntry, error) {
	// Try `ls -l --time-style=long-iso` first (GNU coreutils). If the server
	// doesn't support --time-style (BusyBox), fall back to plain `ls -l`.
	out, err := b.run(fmt.Sprintf("ls -l -A --time-style=long-iso %s 2>/dev/null || ls -l -A %s 2>/dev/null", shellQuote(dir), shellQuote(dir)))
	if err != nil {
		return nil, fmt.Errorf("exec ls: %w", err)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	entries := make([]domain.SshFileEntry, 0, len(lines))
	for _, line := range lines {
		entry, ok := parseLsLongLine(line)
		if !ok {
			continue
		}
		entry.FullPath = path.Join(dir, entry.Name)
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return entries[i].Name < entries[j].Name
	})
	return entries, nil
}

// parseLsLongLine parses a single line from `ls -l` output into an
// SshFileEntry. Supports GNU long-iso and BusyBox/BSD date formats.
// Ported from r-shell's ls_parser.rs.
func parseLsLongLine(line string) (domain.SshFileEntry, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return domain.SshFileEntry{}, false
	}
	tokens := strings.Fields(line)
	if len(tokens) < 2 {
		return domain.SshFileEntry{}, false
	}
	// Skip "total NNN" summary lines
	if strings.EqualFold(tokens[0], "total") {
		return domain.SshFileEntry{}, false
	}
	// Must start with a perms-like token
	perms := tokens[0]
	if !isPermsToken(perms) {
		return domain.SshFileEntry{}, false
	}
	isDir := perms[0] == 'd'

	// Locate the date field
	dateStart, dateLen, modified := locateDateFields(tokens)
	if dateStart < 0 {
		return domain.SshFileEntry{}, false
	}
	nameEnd := dateStart + dateLen
	if nameEnd >= len(tokens) {
		return domain.SshFileEntry{}, false
	}
	name := strings.Join(tokens[nameEnd:], " ")
	// Strip symlink target
	if idx := strings.Index(name, " -> "); idx >= 0 {
		name = name[:idx]
	}
	if name == "" || name == "." || name == ".." {
		return domain.SshFileEntry{}, false
	}

	// Size is the rightmost numeric token before date
	size := int64(0)
	for i := dateStart - 1; i >= 1; i-- {
		if n, err := strconv.ParseInt(tokens[i], 10, 64); err == nil {
			size = n
			break
		}
	}

	return domain.SshFileEntry{
		Name:     name,
		IsDir:    isDir,
		Size:     size,
		Mode:     perms,
		Modified: modified,
	}, true
}

func isPermsToken(s string) bool {
	if len(s) < 10 || len(s) > 11 {
		return false
	}
	if s[0] != 'd' && s[0] != 'l' && s[0] != '-' {
		return false
	}
	for i := 1; i < 9; i++ {
		c := s[i]
		if c != 'r' && c != 'w' && c != 'x' && c != '-' {
			return false
		}
	}
	return true
}

// locateDateFields returns (startIndex, tokenCount, parsedDate).
// Tries GNU long-iso (YYYY-MM-DD HH:MM = 2 tokens), then BusyBox/BSD
// (Mon DD HH:MM or Mon DD YYYY = 3 tokens).
func locateDateFields(tokens []string) (int, int, string) {
	// GNU long-iso
	for i := 0; i+2 < len(tokens); i++ {
		if isLongIsoDate(tokens[i]) && isHHMM(tokens[i+1]) {
			return i, 2, tokens[i] + " " + tokens[i+1]
		}
	}
	// BusyBox/BSD: Mon DD HH:MM|YYYY
	monthNames := map[string]bool{
		"Jan": true, "Feb": true, "Mar": true, "Apr": true, "May": true, "Jun": true,
		"Jul": true, "Aug": true, "Sep": true, "Oct": true, "Nov": true, "Dec": true,
	}
	for i := 0; i+3 < len(tokens); i++ {
		if monthNames[tokens[i]] {
			day := tokens[i+1]
			if _, err := strconv.Atoi(day); err == nil {
				rest := tokens[i+2]
				if strings.Contains(rest, ":") || (len(rest) == 4 && isAllDigits(rest)) {
					return i, 3, tokens[i] + " " + day + " " + rest
				}
			}
		}
	}
	return -1, 0, ""
}

func isLongIsoDate(s string) bool {
	return len(s) == 10 && s[4] == '-' && s[7] == '-' &&
		isAllDigits(s[:4]) && isAllDigits(s[5:7]) && isAllDigits(s[8:10])
}

func isHHMM(s string) bool {
	return len(s) == 5 && s[2] == ':' && isAllDigits(s[:2]) && isAllDigits(s[3:5])
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func (b *execBackend) Read(p string) ([]byte, error) {
	sess, err := b.sshClient.NewSession()
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	var buf bytes.Buffer
	sess.Stdout = &buf
	if err := sess.Run(fmt.Sprintf("cat %s", shellQuote(p))); err != nil {
		return nil, fmt.Errorf("exec cat: %w", err)
	}
	return buf.Bytes(), nil
}

func (b *execBackend) Write(p string, content []byte) error {
	sess, err := b.sshClient.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()
	stdin, err := sess.StdinPipe()
	if err != nil {
		return err
	}
	go func() {
		stdin.Write(content)
		stdin.Close()
	}()
	return sess.Run(fmt.Sprintf("cat > %s", shellQuote(p)))
}

func (b *execBackend) Mkdir(p string) error {
	_, err := b.run(fmt.Sprintf("mkdir -p %s", shellQuote(p)))
	return err
}

func (b *execBackend) Remove(p string) error {
	_, err := b.run(fmt.Sprintf("rm -f %s", shellQuote(p)))
	return err
}

func (b *execBackend) RemoveDir(p string) error {
	_, err := b.run(fmt.Sprintf("rm -rf %s", shellQuote(p)))
	return err
}

func (b *execBackend) Rename(from, to string) error {
	_, err := b.run(fmt.Sprintf("mv %s %s", shellQuote(from), shellQuote(to)))
	return err
}

func (b *execBackend) Chmod(p string, mode os.FileMode) error {
	_, err := b.run(fmt.Sprintf("chmod %o %s", mode.Perm(), shellQuote(p)))
	return err
}

func (b *execBackend) Stat(p string) (string, int64, bool, string, time.Time, error) {
	// name size mode mtime — tab-separated
	out, err := b.run(fmt.Sprintf(
		`name=$(basename %s); info=$(stat -c "%%n\t%%s\t%%F\t%%Y" %s 2>/dev/null || stat -f "%%N\t%%z\t%%HT\t%%m" %s 2>/dev/null); echo "$info"`,
		shellQuote(p), shellQuote(p), shellQuote(p),
	))
	if err != nil {
		return "", 0, false, "", time.Time{}, fmt.Errorf("exec stat: %w", err)
	}
	parts := strings.Split(strings.TrimSpace(out), "\t")
	if len(parts) < 4 {
		return "", 0, false, "", time.Time{}, fmt.Errorf("stat: unexpected output %q", out)
	}
	size, _ := strconv.ParseInt(parts[1], 10, 64)
	ts, _ := strconv.ParseInt(parts[3], 10, 64)
	isDir := strings.Contains(strings.ToLower(parts[2]), "dir")
	return parts[0], size, isDir, "", time.Unix(ts, 0), nil
}

func (b *execBackend) Lstat(p string) (string, int64, bool, string, time.Time, error) {
	// Use stat without -L so symlinks are reported as symlinks, not followed.
	out, err := b.run(fmt.Sprintf(
		`name=$(basename %s); info=$(stat -c "%%n\t%%s\t%%F\t%%Y" %s 2>/dev/null || stat -f "%%N\t%%z\t%%HT\t%%m" %s 2>/dev/null); echo "$info"`,
		shellQuote(p), shellQuote(p), shellQuote(p),
	))
	if err != nil {
		return "", 0, false, "", time.Time{}, fmt.Errorf("exec lstat: %w", err)
	}
	parts := strings.Split(strings.TrimSpace(out), "\t")
	if len(parts) < 4 {
		return "", 0, false, "", time.Time{}, fmt.Errorf("lstat: unexpected output %q", out)
	}
	size, _ := strconv.ParseInt(parts[1], 10, 64)
	ts, _ := strconv.ParseInt(parts[3], 10, 64)
	// For symlinks, %F returns "symbolic link" — not a directory.
	isDir := strings.Contains(strings.ToLower(parts[2]), "dir")
	return parts[0], size, isDir, parts[2], time.Unix(ts, 0), nil
}

func (b *execBackend) Close() error { return nil }
