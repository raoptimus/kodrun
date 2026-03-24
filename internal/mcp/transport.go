package mcp

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/pkg/errors"
)

const (
	stderrBufferSize = 4096
	receiveTimeout   = 60 * time.Second
	shutdownTimeout  = 3 * time.Second
	killTimeout      = 2 * time.Second
)

var validEnvKey = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// Transport is the communication layer for MCP protocol.
type Transport interface {
	Send(req Request) error
	Receive(ctx context.Context) (Response, error)
	Close() error
}

// StdioTransport communicates with an MCP server via stdin/stdout of a subprocess.
type StdioTransport struct {
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	stdout       *bufio.Reader
	stdoutCloser io.Closer  // underlying stdout pipe, closed to unblock decode goroutines
	closeStdout  sync.Once  // prevents double-close of stdoutCloser
	stderr       *ringBuffer
	mu           sync.Mutex // protects stdin writes
	closed       chan struct{}
}

// NewStdioTransport starts a subprocess and wires stdin/stdout for JSON-RPC communication.
func NewStdioTransport(command string, args []string, env map[string]string, workDir string) (*StdioTransport, error) {
	cmd := exec.Command(command, args...)
	cmd.Dir = workDir

	// Merge env vars with key validation.
	cmd.Env = os.Environ()
	for k, v := range env {
		upper := strings.ToUpper(k)
		if !validEnvKey.MatchString(k) || strings.HasPrefix(upper, "LD_") || strings.HasPrefix(upper, "DYLD_") {
			return nil, errors.Errorf("invalid env key %q", k)
		}
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, errors.WithMessage(err, "stdin pipe")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, errors.WithMessage(err, "stdout pipe")
	}

	stderrBuf := newRingBuffer(stderrBufferSize)
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		return nil, errors.WithMessagef(err, "start %s", command)
	}

	return &StdioTransport{
		cmd:          cmd,
		stdin:        stdin,
		stdout:       bufio.NewReader(stdout),
		stdoutCloser: stdout,
		stderr:       stderrBuf,
		closed:       make(chan struct{}),
	}, nil
}

// Send writes a JSON-RPC request to the subprocess stdin.
func (t *StdioTransport) Send(req Request) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return encode(t.stdin, req)
}

// Receive reads a JSON-RPC response from the subprocess stdout.
// Blocks until a response is available, the context is cancelled, or the read timeout (60s) expires.
// On timeout or context cancellation, the stdout pipe is closed to unblock the goroutine.
func (t *StdioTransport) Receive(ctx context.Context) (Response, error) {
	type result struct {
		resp Response
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		resp, err := decode(t.stdout)
		ch <- result{resp, err}
	}()

	select {
	case r := <-ch:
		return r.resp, r.err
	case <-t.closed:
		return Response{}, errors.New("transport closed")
	case <-ctx.Done():
		// Close stdout to unblock the decode goroutine.
		t.closeStdout.Do(func() { _ = t.stdoutCloser.Close() })
		return Response{}, ctx.Err()
	case <-time.After(receiveTimeout):
		// Close stdout to unblock the decode goroutine.
		t.closeStdout.Do(func() { _ = t.stdoutCloser.Close() })
		return Response{}, errors.Errorf("receive timeout (%s)", receiveTimeout)
	}
}

// Close terminates the subprocess gracefully.
func (t *StdioTransport) Close() error {
	// Signal all Receive() callers that we're closing.
	select {
	case <-t.closed:
	default:
		close(t.closed)
	}

	_ = t.stdin.Close()
	// Close stdout pipe to unblock any goroutines blocked in decode().
	t.closeStdout.Do(func() { _ = t.stdoutCloser.Close() })

	if t.cmd.Process != nil {
		// Try SIGINT first, then force kill.
		_ = t.cmd.Process.Signal(os.Interrupt)
		done := make(chan error, 1)
		go func() { done <- t.cmd.Wait() }()

		select {
		case <-done:
		case <-time.After(shutdownTimeout):
			_ = t.cmd.Process.Kill()
			select {
			case <-done:
			case <-time.After(killTimeout):
				// Process is a zombie, give up waiting.
			}
		}
	}
	return nil
}

// Stderr returns captured stderr output from the subprocess.
func (t *StdioTransport) Stderr() string {
	return t.stderr.String()
}

// ringBuffer is a fixed-size circular buffer implementing io.Writer.
type ringBuffer struct {
	buf  []byte
	pos  int
	full bool
	mu   sync.Mutex
}

func newRingBuffer(size int) *ringBuffer {
	return &ringBuffer{buf: make([]byte, size)}
}

func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(p)
	size := len(r.buf)
	if n == 0 {
		return 0, nil
	}
	if n >= size {
		copy(r.buf, p[n-size:])
		r.pos = 0
		r.full = true
		return n, nil
	}
	space := size - r.pos
	if n <= space {
		copy(r.buf[r.pos:], p)
	} else {
		copy(r.buf[r.pos:], p[:space])
		copy(r.buf, p[space:])
	}
	newPos := (r.pos + n) % size
	if !r.full && newPos <= r.pos {
		r.full = true
	}
	r.pos = newPos
	return n, nil
}

func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.full {
		return string(r.buf[:r.pos])
	}
	return string(r.buf[r.pos:]) + string(r.buf[:r.pos])
}
