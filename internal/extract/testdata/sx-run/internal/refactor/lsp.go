package refactor

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf16"
	"unicode/utf8"
)

// LSP is one gopls session for a whole run, spoken to over the Language
// Server Protocol on its standard input and output.
//
// Launching "gopls codeaction" for every change type-checked the program
// from scratch each time: 2.7 seconds a call on cc-connect's core package. A
// shared daemon (gopls -remote=auto) was slower still, 4.1 seconds, because
// every command-line call opens a new session in it. One session kept open
// for the run keeps the program loaded; what it has to be told is which
// files changed since the last request, and sx tells it.
type LSP struct {
	root    string
	cmd     *exec.Cmd
	in      io.WriteCloser
	mu      sync.Mutex // guards writes and the pending table
	next    int
	wait    map[int]chan rpcResponse
	files   map[string][sha256.Size]byte // what gopls was last told each file holds
	version int                          // of the documents sx opens
	done    chan struct{}
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// session, when set, is where Apply and the repair step send gopls's code
// actions instead of launching gopls for each one.
var session *LSP

// UseLSP routes gopls's code actions through l, or back to the command line
// when l is nil.
func UseLSP(l *LSP) { session = l }

// StartLSP starts gopls on the module under root.
func StartLSP(goplsPath, root string) (*LSP, error) {
	cmd := exec.Command(goplsPath, "serve")
	cmd.Dir = root
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	l := &LSP{root: root, cmd: cmd, in: in, wait: map[int]chan rpcResponse{}, done: make(chan struct{})}
	go l.read(bufio.NewReader(out))
	uri := fileURI(root)
	init := map[string]any{
		"processId":        os.Getpid(),
		"rootUri":          uri,
		"workspaceFolders": []map[string]string{{"uri": uri, "name": filepath.Base(root)}},
		"capabilities": map[string]any{
			"workspace": map[string]any{
				"workspaceEdit":          map[string]any{"documentChanges": true},
				"didChangeWatchedFiles":  map[string]any{"dynamicRegistration": false},
				"configuration":          true,
				"workspaceFolders":       true,
				"applyEdit":              false,
				"executeCommand":         map[string]any{},
				"didChangeConfiguration": map[string]any{},
			},
			"textDocument": map[string]any{
				"codeAction": map[string]any{
					"codeActionLiteralSupport": map[string]any{"codeActionKind": map[string]any{"valueSet": []string{
						"refactor", "refactor.extract", "refactor.inline", "source", "source.organizeImports",
					}}},
					"resolveSupport": map[string]any{"properties": []string{"edit"}},
					"dataSupport":    true,
				},
			},
		},
	}
	if _, err := l.call("initialize", init); err != nil {
		l.Close()
		return nil, fmt.Errorf("gopls initialize: %w", err)
	}
	if err := l.notify("initialized", map[string]any{}); err != nil {
		l.Close()
		return nil, err
	}
	l.files = snapshotGo(root)
	return l, nil
}

// Close ends the session.
func (l *LSP) Close() {
	l.call("shutdown", nil)
	l.notify("exit", nil)
	l.in.Close()
	l.cmd.Wait()
}

func fileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func uriPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return strings.TrimPrefix(uri, "file://")
	}
	return u.Path
}

func (l *LSP) write(msg map[string]any) error {
	msg["jsonrpc"] = "2.0"
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(l.in, "Content-Length: %d\r\n\r\n%s", len(b), b)
	return err
}

func (l *LSP) notify(method string, params any) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.write(map[string]any{"method": method, "params": params})
}

func (l *LSP) call(method string, params any) (json.RawMessage, error) {
	l.mu.Lock()
	l.next++
	id := l.next
	ch := make(chan rpcResponse, 1)
	l.wait[id] = ch
	err := l.write(map[string]any{"id": id, "method": method, "params": params})
	l.mu.Unlock()
	if err != nil {
		return nil, err
	}
	select {
	case r := <-ch:
		if r.Error != nil {
			return nil, fmt.Errorf("%s: %s", method, r.Error.Message)
		}
		return r.Result, nil
	case <-l.done:
		return nil, fmt.Errorf("gopls exited")
	}
}

// read dispatches what gopls sends: responses to our calls, and its own
// requests, which get an empty answer so it never waits on us.
func (l *LSP) read(r *bufio.Reader) {
	defer close(l.done)
	for {
		length := 0
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			if v, ok := strings.CutPrefix(line, "Content-Length: "); ok {
				length, _ = strconv.Atoi(v)
			}
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(r, body); err != nil {
			return
		}
		var msg struct {
			ID     *json.RawMessage `json:"id"`
			Method string           `json:"method"`
			Params json.RawMessage  `json:"params"`
			rpcResponse
		}
		if json.Unmarshal(body, &msg) != nil {
			continue
		}
		switch {
		case msg.ID != nil && msg.Method != "":
			var result any
			if msg.Method == "workspace/configuration" {
				var p struct{ Items []any }
				json.Unmarshal(msg.Params, &p)
				result = make([]any, len(p.Items))
			}
			l.mu.Lock()
			l.write(map[string]any{"id": msg.ID, "result": result})
			l.mu.Unlock()
		case msg.ID != nil:
			var id int
			json.Unmarshal(*msg.ID, &id)
			l.mu.Lock()
			ch := l.wait[id]
			delete(l.wait, id)
			l.mu.Unlock()
			if ch != nil {
				ch <- msg.rpcResponse
			}
		}
	}
}

// snapshotGo is the content hash of every Go file and go.mod under root.
func snapshotGo(root string) map[string][sha256.Size]byte {
	out := map[string][sha256.Size]byte{}
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") || d.Name() == "go.mod" {
			if b, err := os.ReadFile(path); err == nil {
				out[path] = sha256.Sum256(b)
			}
		}
		return nil
	})
	return out
}

// sync tells gopls about every file that changed on disk since it was last
// told: sx writes, reverts and formats files itself, and a session that has
// not heard of a change edits a program that no longer exists.
func (l *LSP) sync() error {
	now := snapshotGo(l.root)
	var changes []map[string]any
	for path, h := range now {
		if old, ok := l.files[path]; !ok {
			changes = append(changes, map[string]any{"uri": fileURI(path), "type": 1})
		} else if old != h {
			changes = append(changes, map[string]any{"uri": fileURI(path), "type": 2})
		}
	}
	for path := range l.files {
		if _, ok := now[path]; !ok {
			changes = append(changes, map[string]any{"uri": fileURI(path), "type": 3})
		}
	}
	l.files = now
	if len(changes) == 0 {
		return nil
	}
	return l.notify("workspace/didChangeWatchedFiles", map[string]any{"changes": changes})
}

// position is an LSP position - zero-based line, UTF-16 column - for a
// one-based line and byte column in src.
func position(src []byte, line, col int) map[string]int {
	lines := bytes.SplitAfter(src, []byte("\n"))
	if line-1 >= len(lines) {
		return map[string]int{"line": line - 1, "character": 0}
	}
	prefix := lines[line-1]
	if col-1 < len(prefix) {
		prefix = prefix[:col-1]
	}
	units := 0
	for len(prefix) > 0 {
		r, n := utf8.DecodeRune(prefix)
		units += len(utf16.Encode([]rune{r}))
		prefix = prefix[n:]
	}
	return map[string]int{"line": line - 1, "character": units}
}

// offset is the byte offset in src of an LSP position.
func offset(src []byte, line, character int) int {
	off := 0
	for i := 0; i < line; i++ {
		j := bytes.IndexByte(src[off:], '\n')
		if j < 0 {
			return len(src)
		}
		off += j + 1
	}
	for units := 0; units < character && off < len(src) && src[off] != '\n'; {
		r, n := utf8.DecodeRune(src[off:])
		units += len(utf16.Encode([]rune{r}))
		off += n
	}
	return off
}

// CodeAction asks for the code action of the given kind over a range of a
// file (one-based lines, byte columns), and writes its edits to disk.
func (l *LSP) CodeAction(path, kind string, startLine, startCol, endLine, endCol int) error {
	if err := l.sync(); err != nil {
		return err
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// The file being edited is opened with its current text, which gopls
	// takes as authoritative; a change notice alone is processed in its own
	// time, and under load a request could still see the file before it.
	l.version++
	doc := map[string]any{"uri": fileURI(path), "languageId": "go", "version": l.version, "text": string(src)}
	if err := l.notify("textDocument/didOpen", map[string]any{"textDocument": doc}); err != nil {
		return err
	}
	defer l.notify("textDocument/didClose", map[string]any{"textDocument": map[string]string{"uri": fileURI(path)}})
	res, err := l.call("textDocument/codeAction", map[string]any{
		"textDocument": map[string]string{"uri": fileURI(path)},
		"range":        map[string]any{"start": position(src, startLine, startCol), "end": position(src, endLine, endCol)},
		"context":      map[string]any{"diagnostics": []any{}, "only": []string{kind}, "triggerKind": 1},
	})
	if err != nil {
		return err
	}
	var actions []json.RawMessage
	if err := json.Unmarshal(res, &actions); err != nil {
		return err
	}
	for _, raw := range actions {
		var a struct {
			Kind string          `json:"kind"`
			Edit json.RawMessage `json:"edit"`
		}
		json.Unmarshal(raw, &a)
		if a.Kind != kind {
			continue
		}
		if len(a.Edit) == 0 || string(a.Edit) == "null" {
			resolved, err := l.call("codeAction/resolve", json.RawMessage(raw))
			if err != nil {
				return err
			}
			json.Unmarshal(resolved, &a)
		}
		if len(a.Edit) == 0 || string(a.Edit) == "null" {
			return fmt.Errorf("gopls offered %s with no edit", kind)
		}
		if err := applyWorkspaceEdit(a.Edit); err != nil {
			return err
		}
		// gopls computed the edit but did not make it: until it hears
		// that the file changed, its view is the file before the edit.
		return l.sync()
	}
	return fmt.Errorf("gopls offered no %s here", kind)
}

// applyWorkspaceEdit writes a WorkspaceEdit's text edits to disk.
func applyWorkspaceEdit(raw json.RawMessage) error {
	type textEdit struct {
		Range struct {
			Start struct{ Line, Character int } `json:"start"`
			End   struct{ Line, Character int } `json:"end"`
		} `json:"range"`
		NewText string `json:"newText"`
	}
	var we struct {
		Changes         map[string][]textEdit `json:"changes"`
		DocumentChanges []struct {
			TextDocument struct{ URI string } `json:"textDocument"`
			Edits        []textEdit           `json:"edits"`
		} `json:"documentChanges"`
	}
	if err := json.Unmarshal(raw, &we); err != nil {
		return err
	}
	byFile := map[string][]textEdit{}
	for uri, edits := range we.Changes {
		byFile[uriPath(uri)] = append(byFile[uriPath(uri)], edits...)
	}
	for _, dc := range we.DocumentChanges {
		byFile[uriPath(dc.TextDocument.URI)] = append(byFile[uriPath(dc.TextDocument.URI)], dc.Edits...)
	}
	for path, edits := range byFile {
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		type span struct {
			start, end int
			text       string
		}
		var spans []span
		for _, e := range edits {
			spans = append(spans, span{
				offset(src, e.Range.Start.Line, e.Range.Start.Character),
				offset(src, e.Range.End.Line, e.Range.End.Character),
				e.NewText,
			})
		}
		// Applied last to first, so earlier offsets stay valid.
		sort.SliceStable(spans, func(i, j int) bool { return spans[i].start > spans[j].start })
		out := src
		for _, s := range spans {
			out = append(append(append([]byte{}, out[:s.start]...), s.text...), out[s.end:]...)
		}
		if err := os.WriteFile(path, out, 0o644); err != nil {
			return err
		}
	}
	return nil
}
