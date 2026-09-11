package xssh

import (
	"fmt"
	"os/exec"
)

// A pipe shell keeps command-line access available without a platform PTY.
// It deliberately does not claim terminal semantics or support full-screen apps.
func attachPipeShell(sess *Session) error {
	cfg := sess.Config()
	var args []string
	switch shellBase(cfg.Shell) {
	case "powershell", "pwsh":
		args = []string{"-NoLogo", "-NoProfile", "-Command", "-"}
	case "cmd":
		args = []string{"/Q", "/D"}
	}
	cmd := exec.Command(cfg.Shell, args...)
	prepareCommand(cmd)
	cmd.Dir, cmd.Env = cfg.WorkingDirectory, sess.Env
	cmd.Stdout, cmd.Stderr = sess.Channel, sess.Channel.Stderr()
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("pipe shell stdin: %w", err)
	}
	if err := cmd.Start(); err != nil {
		stdin.Close()
		return fmt.Errorf("start pipe shell: %w", err)
	}
	debugf(sess, "Basic pipe shell attached (no terminal emulation)")
	sess.goTask(func() { copyCommandStdin(sess, stdin) })
	sess.goTask(func() {
		defer sess.Channel.Close()
		err, lifecycleErr := waitCommand(cmd, sess.Done())
		if lifecycleErr != nil {
			debugf(sess, "Pipe shell lifecycle: %v", lifecycleErr)
		}
		sendExitStatus(sess, commandExitCode(err))
	})
	return nil
}
