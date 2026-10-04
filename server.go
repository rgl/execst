package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/gliderlabs/ssh"
	gossh "golang.org/x/crypto/ssh"
)

func execServer(args []string) {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	pipePath := fs.String("pipe", "", "named pipe path (required)")
	clientSID := fs.String("client-sid", "", "client user SID (S-1-5-...)")
	clientAuthorizedKey := fs.String("client-authorized-key", "", "client SSH public key")
	serverLog := fs.String("server-log", "", "path for the server log file (default: discard)")
	fs.Parse(args)

	if *pipePath == "" || *clientSID == "" || *clientAuthorizedKey == "" {
		fs.Usage()
		os.Exit(2)
	}

	if err := setupServerLogging(*serverLog); err != nil {
		fmt.Fprintf(os.Stderr, "server: log setup failed: %v\n", err)
	}

	authorizedPublicKey, _, _, _, err := gossh.ParseAuthorizedKey([]byte(*clientAuthorizedKey))
	if err != nil {
		log.Fatalf("invalid authorized-key: %v", err)
	}
	authorized := gossh.MarshalAuthorizedKey(authorizedPublicKey)

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		log.Fatal(err)
	}
	signer, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		log.Fatal(err)
	}

	serverSID, err := getCurrentUserSID()
	if err != nil {
		log.Fatalf("resolve server SID: %v", err)
	}
	sd := fmt.Sprintf("D:P(A;;GA;;;%s)(A;;GA;;;%s)", serverSID, *clientSID)

	l, err := winio.ListenPipe(*pipePath, &winio.PipeConfig{
		SecurityDescriptor: sd,
		InputBufferSize:    64 * 1024,
		OutputBufferSize:   64 * 1024,
	})
	if err != nil {
		log.Fatalf("ListenPipe: %v", err)
	}
	defer l.Close()

	srv := &ssh.Server{
		Handler: func(s ssh.Session) {
			sigCh := make(chan ssh.Signal, 1)
			s.Signals(sigCh)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			go func() {
				for sig := range sigCh {
					if sig == ssh.SIGINT {
						log.Println("server: SIGINT received, cancelling command")
						cancel()
						return
					}
				}
			}()

			argv := s.Command()
			if len(argv) == 0 {
				s.Exit(1)
				return
			}

			exitCode := execCommand(ctx, s, argv)
			_ = s.Exit(exitCode)

			time.Sleep(150 * time.Millisecond)
			os.Exit(0)
		},
		PublicKeyHandler: func(ctx ssh.Context, key ssh.PublicKey) bool {
			return subtle.ConstantTimeCompare(gossh.MarshalAuthorizedKey(key), authorized) == 1
		},
		PasswordHandler: nil,
	}
	srv.AddHostKey(signer)

	log.Printf("server: listening on %s (server SID %s, client SID %s)", *pipePath, serverSID, *clientSID)
	err = srv.Serve(l)
	if err != nil && !errors.Is(err, ssh.ErrServerClosed) {
		log.Fatalf("Serve: %v", err)
	}
	log.Printf("server: done")
}

// TODO once https://github.com/golang/go/issues/80415 lands, put the process in a job object.
func execCommand(ctx context.Context, s ssh.Session, argv []string) int {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = s
	cmd.Stdout = s
	cmd.Stderr = s.Stderr()
	cmd.Env = append(os.Environ(), s.Environ()...)

	if err := cmd.Start(); err != nil {
		fmt.Fprintf(s.Stderr(), "server: %v\n", err)
		return 1
	}

	err := cmd.Wait()
	if err != nil {
		if ctx.Err() != nil {
			return 130
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(s.Stderr(), "server: %v\n", err)
		return 1
	}

	return 0
}

func setupServerLogging(serverLog string) error {
	if serverLog == "" {
		log.SetOutput(io.Discard)
		return nil
	}

	f, err := os.OpenFile(serverLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log %s: %w", serverLog, err)
	}

	log.SetOutput(f)
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Printf("server: logging to %s", serverLog)

	return nil
}
