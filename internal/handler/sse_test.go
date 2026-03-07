package handler_test

import (
	"bufio"
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/emilhauk/msg/internal/model"
	"github.com/emilhauk/msg/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleSSE_InitialConnect(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	ts.GrantAccess(t, testRoom, alice.ID)
	cookie := ts.AuthCookie(t, alice)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", ts.Server.URL+"/rooms/"+testRoom+"/events", nil)
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 64*1024)

	// The SSE handler writes ": connected\n\n" then "event: version\ndata: test\n\n"
	// and flushes. Reading the first 5 lines covers both.
	lines := readSSELines(t, scanner, 5)

	assert.Equal(t, ": connected", lines[0])
	assert.Equal(t, "", lines[1])
	assert.Equal(t, "event: version", lines[2])
	assert.Equal(t, "data: test", lines[3])
	assert.Equal(t, "", lines[4])
}

func TestHandleSSE_PubSubRelay(t *testing.T) {
	ts := testutil.NewTestServer(t)
	ts.SeedRoom(t, model.Room{ID: testRoom, Name: "Test Room"})
	ts.GrantAccess(t, testRoom, alice.ID)
	cookie := ts.AuthCookie(t, alice)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", ts.Server.URL+"/rooms/"+testRoom+"/events", nil)
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 64*1024)

	// Drain the initial ": connected" + "" + "event: version" + "data: test" + ""
	readSSELines(t, scanner, 5)

	// The SSE handler flushes initial events BEFORE calling Subscribe. Give the
	// handler goroutine a moment to establish the subscription so the published
	// message isn't missed.
	time.Sleep(50 * time.Millisecond)

	// Publish a message directly to Redis.
	require.NoError(t, ts.Redis.Publish(ctx, testRoom, "msg:<p>hello sse</p>"))

	// The handler should relay it as an SSE message event.
	lines := readSSELines(t, scanner, 3)
	assert.Equal(t, "event: message", lines[0])
	assert.Contains(t, lines[1], "data: ")
	assert.Contains(t, lines[1], "<p>hello sse</p>")
	assert.Equal(t, "", lines[2])
}

// readSSELines reads exactly n lines from scanner, failing the test on timeout.
func readSSELines(t *testing.T, scanner *bufio.Scanner, n int) []string {
	t.Helper()
	lines := make([]string, 0, n)
	for len(lines) < n {
		require.True(t, scanner.Scan(), "expected more SSE lines; got %d of %d", len(lines), n)
		lines = append(lines, scanner.Text())
	}
	return lines
}
