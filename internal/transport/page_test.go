package transport_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/nono/scut/internal/transport"
)

func items(n int) []json.RawMessage {
	out := make([]json.RawMessage, n)
	for i := range out {
		out[i] = json.RawMessage(`{}`)
	}
	return out
}

func TestPagerRepeatCursor(t *testing.T) {
	calls := 0
	got, err := transport.Page(context.Background(), func(string) ([]json.RawMessage, string, error) {
		calls++
		return items(1), "string", nil
	}, -1)
	if err != nil || calls != 2 || len(got) != 2 {
		t.Errorf("calls %d, items %d, err %v", calls, len(got), err)
	}
}

func TestPagerCap(t *testing.T) {
	calls := 0
	got, _ := transport.Page(context.Background(), func(string) ([]json.RawMessage, string, error) {
		calls++
		return items(1), fmt.Sprint("c", calls), nil
	}, -1)
	if calls != 100 || len(got) != 100 {
		t.Errorf("calls %d, items %d", calls, len(got))
	}
}

func TestPagerLimit(t *testing.T) {
	calls := 0
	got, _ := transport.Page(context.Background(), func(string) ([]json.RawMessage, string, error) {
		calls++
		return items(25), fmt.Sprint("c", calls), nil
	}, 30)
	if calls != 2 || len(got) != 30 {
		t.Errorf("calls %d, items %d", calls, len(got))
	}
}

func TestPagerStopsOnEmptyCursor(t *testing.T) {
	calls := 0
	got, _ := transport.Page(context.Background(), func(string) ([]json.RawMessage, string, error) {
		calls++
		return items(3), "", nil
	}, -1)
	if calls != 1 || len(got) != 3 {
		t.Errorf("calls %d, items %d", calls, len(got))
	}
}
