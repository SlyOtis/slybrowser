package slyb

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/nimling/nimbody/cli"
)

const callTimeout = 2 * time.Minute

var ErrNotRunning = errors.New("slybrowser is not running")

func notRunning(err error) bool {
	return errors.Is(err, ErrNotRunning)
}

type request struct {
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

type reply struct {
	Result json.RawMessage `json:"result"`
	Error  string          `json:"error"`
}

func Home() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".slyb"), nil
}

func SocketPath() (string, error) {
	home, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "browser.sock"), nil
}

func Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	path, err := SocketPath()
	if err != nil {
		return nil, err
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, &cli.Error{Code: cli.ExitFailure, Message: "slybrowser is not running", Hint: "start Zen; if it is open, run `slyb install` and restart Zen", Cause: errors.Join(ErrNotRunning, err)}
	}
	defer conn.Close()
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(callTimeout)
	}
	_ = conn.SetDeadline(deadline)
	if err := json.NewEncoder(conn).Encode(request{Method: method, Params: params}); err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(conn)
	if err != nil {
		return nil, &cli.Error{Code: cli.ExitFailure, Message: "slybrowser did not answer " + method, Hint: "slyb status shows whether the browser is responding", Cause: err}
	}
	var answer reply
	if err := json.Unmarshal(raw, &answer); err != nil {
		return nil, &cli.Error{Code: cli.ExitFailure, Message: "slybrowser sent an unreadable reply", Cause: err}
	}
	if answer.Error != "" {
		return nil, cli.Rejected(answer.Error, "")
	}
	return answer.Result, nil
}

func CallInto(ctx context.Context, method string, params any, out any) error {
	raw, err := Call(ctx, method, params)
	if err != nil || out == nil {
		return err
	}
	return json.Unmarshal(raw, out)
}
