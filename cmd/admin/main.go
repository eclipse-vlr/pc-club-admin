package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/term"
	"io"
	"os"
	"pc-club-admin/internal/auth"
	"pc-club-admin/internal/config"
	"pc-club-admin/internal/storage/postgres"
	"strings"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Первичная настройка не выполнена: проверьте конфигурацию, схему БД и отсутствие ранее созданных пользователей. Логин: 3–100 латинских символов a-z, 0-9, ._-. Пароль: минимум 12 символов.")
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) < 2 || os.Args[1] != "bootstrap-manager" {
		return fmt.Errorf("usage: admin bootstrap-manager --login LOGIN --name NAME [--password-stdin]")
	}
	flags := flag.NewFlagSet("bootstrap-manager", flag.ContinueOnError)
	login := flags.String("login", "", "manager login")
	name := flags.String("name", "", "display name")
	stdin := flags.Bool("password-stdin", false, "read password from secret pipe, never a command argument")
	if err := flags.Parse(os.Args[2:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected argument")
	}
	var password string
	if *stdin {
		line, err := bufio.NewReader(io.LimitReader(os.Stdin, 1024)).ReadString('\n')
		if err != nil && err != io.EOF {
			return err
		}
		password = strings.TrimRight(line, "\r\n")
	} else {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return fmt.Errorf("interactive terminal required")
		}
		fmt.Fprint(os.Stderr, "Новый пароль управляющего: ")
		secret, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return err
		}
		password = string(secret)
		clear(secret)
		fmt.Fprint(os.Stderr, "Повторите пароль: ")
		confirm, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return err
		}
		equal := auth.Equal(password, string(confirm))
		clear(confirm)
		if !equal {
			return fmt.Errorf("passwords differ")
		}
	}
	c, err := config.Load()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, c.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := postgres.CheckSchema(ctx, pool, c.Timezone); err != nil {
		return err
	}
	service := auth.New(postgres.AuthRepository{Pool: pool}, c.PINPepper)
	if err := service.Bootstrap(ctx, auth.UserInput{Name: *name, Login: *login, Password: password, Roles: []string{"manager"}}); err != nil {
		return err
	}
	fmt.Println("Управляющий создан. Пароль не сохранён в seed и не выводится.")
	return nil
}
