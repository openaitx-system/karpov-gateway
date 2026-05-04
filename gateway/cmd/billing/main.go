// Billing Service 独立二进制（v0.3 占位）；推荐入口 ./qqmusic-gateway。
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	billingcmd "github.com/MiChongs/QQMusicApi/gateway/internal/cmd/billing"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := billingcmd.Run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
