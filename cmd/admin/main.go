// Command admin manages owner access from a server shell.
//
//	admin enroll            print a single-use passkey registration link (15 min)
//	admin set-backup        set the backup password and TOTP (replaces existing)
//	admin revoke-sessions   end every admin session
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/mdp/qrterminal/v3"
	"golang.org/x/term"

	"thymeandplace/internal/auth"
	"thymeandplace/internal/db"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Getenv, os.Stdin, os.Stdout, os.Stderr, terminalPassword))
}

func terminalPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	return string(b), err
}

const usage = "usage: admin enroll | set-backup | revoke-sessions"

func run(ctx context.Context, args []string, getenv func(string) string, _ io.Reader, stdout, stderr io.Writer, readPassword func(string) (string, error)) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	cmd := args[0]
	if cmd != "enroll" && cmd != "set-backup" && cmd != "revoke-sessions" {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintf(stderr, "admin %s: %v\n", cmd, err)
		return 1
	}
	key, err := auth.ParseSecretKey(getenv("ADMIN_SECRET_KEY"))
	if err != nil {
		return fail(err)
	}
	pool, err := db.Open(ctx, getenv("DATABASE_URL"), db.CLIPool)
	if err != nil {
		return fail(err)
	}
	defer pool.Close()
	rpID := getenv("ADMIN_RP_ID")
	if rpID == "" {
		rpID = "thymeand.place"
	}
	origin := getenv("ADMIN_ORIGIN")
	if origin == "" {
		origin = "https://" + rpID
	}
	// A browser's Origin never ends in "/"; trimming also keeps the enroll
	// link below free of "//admin".
	origin = strings.TrimRight(origin, "/")
	svc, err := auth.NewService(pool, auth.Config{
		RPID: rpID, RPOrigins: []string{origin}, SecretKey: key, BackupEnabled: true,
		Log: slog.New(slog.NewTextHandler(stderr, nil)),
	})
	if err != nil {
		return fail(err)
	}

	switch cmd {
	case "enroll":
		raw, exp, err := svc.CreateEnrollmentToken(ctx)
		if err != nil {
			return fail(err)
		}
		base := strings.TrimRight(getenv("PUBLIC_BASE_URL"), "/")
		if base == "" {
			base = origin
		}
		fmt.Fprintf(stdout, "Open this link within 15 minutes to register a passkey (single use):\n\n  %s/admin/enroll?token=%s\n\nExpires %s UTC.\n",
			base, raw, exp.UTC().Format("2006-01-02 15:04"))
	case "set-backup":
		pw, err := readPassword("New backup password (at least 12 characters): ")
		if err != nil {
			return fail(err)
		}
		again, err := readPassword("Repeat password: ")
		if err != nil {
			return fail(err)
		}
		if pw != again {
			return fail(errors.New("passwords do not match"))
		}
		uri, err := svc.SetBackup(ctx, pw)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "Backup login replaced. Add this to your authenticator app, then clear your terminal:\n\n  %s\n\n", uri)
		qrterminal.GenerateHalfBlock(uri, qrterminal.L, stdout)
	case "revoke-sessions":
		n, err := svc.RevokeAllSessions(ctx)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintf(stdout, "Ended %d session(s).\n", n)
	}
	return 0
}
