package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	_ "github.com/TokenFlux/TokenRouter/ent/runtime"
	"github.com/TokenFlux/TokenRouter/internal/app/bootstrap"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

// run 在返回错误前完成已取得资源的释放。
func run() error {
	email := flag.String("email", "", "Admin email to issue a JWT for (defaults to first active admin)")
	flag.Parse()

	cfg, err := config.LoadForBootstrap()
	if err != nil {
		return fmt.Errorf("failed to load config: %v", err)
	}

	client, sqlDB, err := bootstrap.InitEnt(context.Background(), cfg)
	if err != nil {
		return fmt.Errorf("failed to init db: %v", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			log.Printf("failed to close db: %v", err)
		}
	}()

	access := bootstrap.NewJWTIdentity(client, sqlDB, cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var user *identity.User
	if *email != "" {
		user, err = access.Users.GetByEmail(ctx, *email)
	} else {
		user, err = access.Users.GetFirstAdmin(ctx)
	}
	if err != nil {
		return fmt.Errorf("failed to resolve admin user: %v", err)
	}

	token, err := access.Tokens.GenerateToken(ctx, user)
	if err != nil {
		return fmt.Errorf("failed to generate token: %v", err)
	}

	fmt.Printf("ADMIN_EMAIL=%s\nADMIN_USER_ID=%d\nJWT=%s\n", user.Email, user.ID, token)
	return nil
}
