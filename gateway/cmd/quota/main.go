// Quota Service 独立二进制（v0.3 占位）；推荐入口 ./qqmusic-gateway。
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	quotacmd "github.com/MiChongs/QQMusicApi/gateway/internal/cmd/quota"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := quotacmd.Run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
