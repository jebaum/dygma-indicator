package neuron

import (
	"errors"
	"testing"
)

// fakeTransport is a scripted ReadWriter used to test Query's framing without
// touching a serial port. Each call to Read consumes the next chunk; once the
// chunks are exhausted Read returns (0, nil) (which keeps Query polling until
// it hits its 2s deadline) unless readErr is set.
type fakeTransport struct {
	written []byte
	chunks  [][]byte // each Read returns the next chunk, or 0,nil when exhausted
	readErr error
	writeErr error
}

func (f *fakeTransport) Write(p []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	f.written = append(f.written, p...)
	return len(p), nil
}

func (f *fakeTransport) Read(p []byte) (int, error) {
	if len(f.chunks) == 0 {
		if f.readErr != nil {
			return 0, f.readErr
		}
		return 0, nil
	}
	n := copy(p, f.chunks[0])
	if n < len(f.chunks[0]) {
		f.chunks[0] = f.chunks[0][n:]
	} else {
		f.chunks = f.chunks[1:]
	}
	return n, nil
}

func TestQueryFraming(t *testing.T) {
	tests := []struct {
		name   string
		chunks [][]byte
		want   string
	}{
		{
			name:   "single read response",
			chunks: [][]byte{[]byte("42\r\n.\r\n")},
			want:   "42",
		},
		{
			name:   "split before terminator",
			chunks: [][]byte{[]byte("42\r\n"), []byte(".\r\n")},
			want:   "42",
		},
		{
			name:   "split mid terminator",
			chunks: [][]byte{[]byte("42\r"), []byte("\n.\r"), []byte("\n")},
			want:   "42",
		},
		{
			name:   "empty payload",
			chunks: [][]byte{[]byte("\r\n.\r\n")},
			want:   "",
		},
		{
			name:   "multiline payload",
			chunks: [][]byte{[]byte("line1\r\nline2\r\n.\r\n")},
			want:   "line1\r\nline2",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeTransport{chunks: tc.chunks}
			c := newClient(f)
			got, err := c.Query("test.cmd")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
			if string(f.written) != "test.cmd\n" {
				t.Errorf("written = %q, want %q", string(f.written), "test.cmd\n")
			}
		})
	}
}

func TestQueryWriteError(t *testing.T) {
	wantErr := errors.New("boom")
	f := &fakeTransport{writeErr: wantErr}
	c := newClient(f)
	_, err := c.Query("test.cmd")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("error %v does not wrap %v", err, wantErr)
	}
}

func TestQueryReadError(t *testing.T) {
	wantErr := errors.New("read failed")
	f := &fakeTransport{readErr: wantErr}
	c := newClient(f)
	_, err := c.Query("test.cmd")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("error %v does not wrap %v", err, wantErr)
	}
}
