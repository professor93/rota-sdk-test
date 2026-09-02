// Command login signs one real claude account into a temporary store and
// proves it works end to end: begin the login, finish it with the code,
// then verify by refreshing quota, running one prompt and removing it.
//
// Usage:
//
//	ROTA_HOME=<dir> login begin
//	ROTA_HOME=<dir> login finish <id> <code>
//	ROTA_HOME=<dir> login verify
//
// ROTA_HOME must name an existing directory so a real token never lands in
// ~/.rota by accident.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"

	rota "github.com/professor93/rota/lib"
	"github.com/professor93/rota/rotation"
	"github.com/professor93/rota/store"
	"github.com/professor93/rota/wire"
)

const usage = "usage: login begin | finish <id> <code> | verify"

func main() {
	home := os.Getenv("ROTA_HOME")
	if fi, err := os.Stat(home); home == "" || err != nil || !fi.IsDir() {
		fmt.Fprintln(os.Stderr, "login: ROTA_HOME must be set to an existing directory")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var err error
	switch args := os.Args[1:]; {
	case len(args) == 1 && args[0] == "begin":
		err = begin(ctx)
	case len(args) == 3 && args[0] == "finish":
		err = finish(ctx, args[1], args[2])
	case len(args) == 1 && args[0] == "verify":
		err = verify(ctx)
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "login:", err)
		os.Exit(1)
	}
}

func begin(ctx context.Context) error {
	s, err := store.Open("")
	if err != nil {
		return err
	}
	defer s.Close()
	l, err := s.BeginLogin(ctx, "claude")
	if err != nil {
		return err
	}
	fmt.Println("id:", l.ID)
	fmt.Println("url:", l.URL)
	return nil
}

func finish(ctx context.Context, id, code string) error {
	s, err := store.Open("")
	if err != nil {
		return err
	}
	defer s.Close()
	a, added, err := s.FinishLogin(ctx, id, code)
	if err != nil {
		return err
	}
	fmt.Printf("account: #%d %s added=%t\n", a.ID, a.Label(), added)
	return nil
}

func verify(ctx context.Context) error {
	s, err := store.Open("")
	if err != nil {
		return err
	}
	defer s.Close()
	// verify ends by removing the account, so it refuses to guess which one.
	if len(s.Accounts) != 1 {
		return fmt.Errorf("verify wants exactly one account in the store, found %d", len(s.Accounts))
	}
	a := s.Accounts[0]
	fmt.Printf("account: #%d %s cutoff=%d%%\n", a.ID, a.Label(), rotation.Cutoff(a))

	for _, err := range s.Refresh(ctx, true) {
		fmt.Fprintln(os.Stderr, "refresh:", err)
	}
	d := wire.Describe(a)
	if len(d.Windows) == 0 {
		return errors.New("refresh left no quota windows")
	}
	for _, w := range d.Windows {
		fmt.Printf("window: %s %.1f%% resets in %s\n", w.Name, w.Percent, w.ResetIn)
	}

	// Run releases the store lock before the CLI starts, so the removal
	// below has to reopen.
	spec := rota.Spec{Prompt: "Reply with exactly the word OK.", TimeoutSeconds: 120}
	res, err := s.Run(ctx, a, spec, nil, os.Stdout)
	if err != nil {
		return err
	}
	fmt.Println("result:", res.Result)
	fmt.Println("exit:", res.ExitCode)

	s2, err := store.Open("")
	if err != nil {
		return err
	}
	defer s2.Close()
	if err := s2.Remove(a.ID); err != nil {
		return err
	}
	if err := s2.Save(); err != nil {
		return err
	}
	fmt.Println("removed")
	return nil
}
