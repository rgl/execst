package main

import (
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"github.com/Microsoft/go-winio"
	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/sys/windows"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

func execClient(args []string, cmd []string) {
	fs := flag.NewFlagSet("exec", flag.ExitOnError)
	username := fs.String("username", os.Getenv("EXECST_USERNAME"), "username")
	password := fs.String("password", os.Getenv("EXECST_PASSWORD"), "password")
	attachStdin := fs.Bool("stdin", false, "stdin is attached to the executed command")
	debug := fs.Bool("debug", false, "skip scheduled task; run server directly in-process context")
	clientLog := fs.String("log", "", "path for the client's own log file (default: stderr)")
	serverLog := fs.String("server-log", "", "path for the server's log file (default: discard)")
	env := []string{}
	fs.Func("env", "Set a environment variable (repeatable); omit =VALUE to copy it from the current environment", func(spec string) error {
		name, value, hasValue := strings.Cut(spec, "=")
		if name == "" {
			return fmt.Errorf("environment variable name cannot be empty")
		}
		if !hasValue {
			var ok bool
			if value, ok = os.LookupEnv(name); !ok {
				return fmt.Errorf("environment variable %q does not exist", name)
			}
		}
		env = append(env, name+"="+value)
		return nil
	})
	workdir := fs.String("workdir", "", "Command working directory")
	err := fs.Parse(args)
	if err != nil {
		log.Fatalf("parse flags: %v", err)
	}

	if !*debug && (*username == "" || *password == "") {
		fs.Usage()
		os.Exit(2)
	}

	if *clientLog != "" {
		f, err := os.OpenFile(*clientLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			log.Fatalf("open client log %s: %v", *clientLog, err)
		}
		log.SetOutput(f)
		log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	}

	selfExe, err := os.Executable()
	if err != nil {
		log.Fatalf("resolve self: %v", err)
	}
	exeBase := strings.TrimSuffix(filepath.Base(selfExe), filepath.Ext(selfExe))

	suffix, err := randomHex(8)
	if err != nil {
		log.Fatalf("random suffix: %v", err)
	}
	taskName := fmt.Sprintf("%s-%d-%s", exeBase, os.Getpid(), suffix)
	pipePath := `\\.\pipe\` + taskName

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	signer, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		panic(err)
	}
	pubAuthorized := strings.TrimSpace(string(gossh.MarshalAuthorizedKey(signer.PublicKey())))

	sid, err := getCurrentUserSID()
	if err != nil {
		log.Fatalf("resolve SID: %v", err)
	}

	if *debug {
		log.Printf("debug: spawning server directly (pipe=%s)", pipePath)
		srvArgs := []string{
			"server",
			"--pipe", pipePath,
			"--client-sid", sid,
			"--client-authorized-key", pubAuthorized,
		}
		if *serverLog != "" {
			srvArgs = append(srvArgs, "--server-log", *serverLog)
		}
		srvCmd := exec.Command(selfExe, srvArgs...)
		srvCmd.Stdout = os.Stderr
		srvCmd.Stderr = os.Stderr
		srvCmd.Dir = *workdir
		if err := srvCmd.Start(); err != nil {
			log.Fatalf("spawn server: %v", err)
		}
		defer func() {
			_ = srvCmd.Process.Kill()
			_, _ = srvCmd.Process.Wait()
		}()
	} else {
		if err := registerScheduledTask(taskName, *username, *password, selfExe, pipePath, sid, pubAuthorized, *serverLog, *workdir); err != nil {
			log.Fatalf("register scheduled task: %v", err)
		}
		defer func() { _, _ = execSchtasks("/delete", "/tn", taskName, "/f") }()

		if _, err := execSchtasks("/run", "/tn", taskName); err != nil {
			log.Fatalf("schtasks run: %v", err)
		}

		if err := waitForScheduledTaskRunning(taskName, 1*time.Minute); err != nil {
			log.Fatalf("schtasks wait running: %v", err)
		}
	}

	conn, err := waitForPipe(pipePath, 15*time.Second)
	if err != nil {
		log.Fatalf("dial pipe: %v", err)
	}
	defer conn.Close()

	cfg := &gossh.ClientConfig{
		User:            "client",
		Auth:            []gossh.AuthMethod{gossh.PublicKeys(signer)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
	sshConn, chans, reqs, err := gossh.NewClientConn(conn, "pipe", cfg)
	if err != nil {
		log.Fatalf("ssh handshake: %v", err)
	}
	client := gossh.NewClient(sshConn, chans, reqs)
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		log.Fatal(err)
	}
	defer session.Close()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)
	go func() {
		for range sigCh {
			if err := session.Signal(gossh.SIGINT); err != nil {
				log.Printf("client: signal: %v", err)
			}
		}
	}()

	if *attachStdin {
		session.Stdin = os.Stdin
	}
	session.Stdout = os.Stdout
	session.Stderr = os.Stderr

	for _, e := range env {
		name, value, _ := strings.Cut(e, "=")
		if err := session.Setenv(name, value); err != nil {
			log.Fatalf("setenv %s: %v", name, err)
		}
	}

	if err := session.Run(windows.ComposeCommandLine(cmd)); err != nil {
		log.Fatalf("remote command: %v", err)
	}
}

func randomHex(n int) (string, error) {
	b := make([]byte, n/2)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func registerScheduledTask(taskName, username, password, cmd, pipePath, sid, authorizedKey, serverLog, workdir string) error {
	cmdArgs := fmt.Sprintf(`server --pipe %s --client-sid %s --client-authorized-key "%s"`, pipePath, sid, authorizedKey)
	if serverLog != "" {
		cmdArgs += fmt.Sprintf(` --server-log "%s"`, serverLog)
	}

	workdirXML := ""
	if workdir != "" {
		workdirXML = fmt.Sprintf("<WorkingDirectory>%s</WorkingDirectory>", xmlEscape(workdir))
	}

	taskXML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>%s</Description>
  </RegistrationInfo>
  <Principals>
    <Principal id="Author">
      <UserId>%s</UserId>
      <LogonType>Password</LogonType>
	  <RunLevel>HighestAvailable</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>false</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <IdleSettings>
      <StopOnIdleEnd>false</StopOnIdleEnd>
      <RestartOnIdle>false</RestartOnIdle>
    </IdleSettings>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <RunOnlyIfIdle>false</RunOnlyIfIdle>
    <WakeToRun>false</WakeToRun>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>7</Priority>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>%s</Command>
      <Arguments>%s</Arguments>
	  %s
    </Exec>
  </Actions>
</Task>`,
		xmlEscape(taskName),
		xmlEscape(username),
		xmlEscape(cmd),
		xmlEscape(cmdArgs),
		workdirXML,
	)

	taskFile, err := os.CreateTemp("", fmt.Sprintf("%s-*.xml", taskName))
	if err != nil {
		return fmt.Errorf("create temp xml: %w", err)
	}
	taskFilePath := taskFile.Name()
	defer os.Remove(taskFilePath)
	if err := taskFile.Close(); err != nil {
		return err
	}

	if err := writeUTF16LEWithBOM(taskFilePath, taskXML); err != nil {
		return err
	}

	_, err = execSchtasks(
		"/create",
		"/tn", taskName,
		"/xml", taskFilePath,
		"/ru", username,
		"/rp", password,
		"/f",
	)

	return err
}

func xmlEscape(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
	)
	return r.Replace(s)
}

func writeUTF16LEWithBOM(path, content string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	defer f.Close()

	w := transform.NewWriter(f, unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder())

	if _, err := w.Write([]byte(content)); err != nil {
		w.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("close encoder for %s: %w", path, err)
	}
	return nil
}

func waitForScheduledTaskRunning(taskName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		// NB it also supports csv, but it does not properly escape it, so,
		//    instead, we use list format and try to parse it ourselves...
		out, err := execSchtasks("/query", "/tn", taskName, "/fo", "list", "/v")
		if err != nil {
			return fmt.Errorf("failed to query the scheduled task: %w", err)
		}
		scanner := bufio.NewScanner(strings.NewReader(out))
		for scanner.Scan() {
			line := scanner.Text()
			if len(line) == 0 {
				continue
			}
			key, value, found := strings.Cut(line, ":  ")
			if !found {
				key, value, found = strings.Cut(line, ": ")
				if !found {
					continue
				}
			}
			value = strings.TrimSpace(value)
			if key == "Status" && value == "Running" {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for scheduled task to run: %q", taskName)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func waitForPipe(path string, timeout time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(timeout)
	for {
		c, err := winio.DialPipe(path, nil)
		if err == nil {
			return c, nil
		}
		if time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func execSchtasks(args ...string) (string, error) {
	out, err := exec.Command("schtasks.exe", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("schtasks %v: %v: %s", args, err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func getCurrentUserSID() (string, error) {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return "", fmt.Errorf("OpenProcessToken: %w", err)
	}
	defer token.Close()

	var requiredLen uint32
	err := windows.GetTokenInformation(token, windows.TokenUser, nil, 0, &requiredLen)
	if err != windows.ERROR_INSUFFICIENT_BUFFER {
		return "", fmt.Errorf("GetTokenInformation(size): %w", err)
	}

	buf := make([]byte, requiredLen)
	if err := windows.GetTokenInformation(token, windows.TokenUser, &buf[0], requiredLen, &requiredLen); err != nil {
		return "", fmt.Errorf("GetTokenInformation: %w", err)
	}

	userInfo := (*windows.Tokenuser)(unsafe.Pointer(&buf[0]))
	return userInfo.User.Sid.String(), nil
}
