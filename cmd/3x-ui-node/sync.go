package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/mhsanaei/3x-ui/v3/internal/node"
)

func syncCommand(c node.Config, args []string, out io.Writer) error {
	if len(args) != 1 || args[0] != "preview" {
		return fmt.Errorf("usage: 3x-ui-node sync preview")
	}
	if !c.MasterSync.Enabled {
		return fmt.Errorf("master sync is disabled; configure masterSync first")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	inbounds, err := node.FetchMasterInbounds(ctx, c.MasterSync)
	if err != nil {
		return err
	}
	byID := make(map[int]node.Inbound, len(inbounds))
	for _, inbound := range inbounds {
		byID[inbound.ID] = inbound
	}
	fmt.Fprintln(out, "主面板读取预览（只读，不修改副面板）")
	for _, mapping := range c.MasterSync.Mappings {
		inbound, ok := byID[mapping.MasterInboundID]
		if !ok {
			fmt.Fprintf(out, "[%s] 主面板入站 %d：未找到；本地入站 %d 不会修改\n", mapping.ID, mapping.MasterInboundID, mapping.LocalInboundID)
			continue
		}
		clients, err := inbound.Clients()
		if err != nil {
			return fmt.Errorf("mapping %s: parse clients: %w", mapping.ID, err)
		}
		fmt.Fprintf(out, "[%s] 主面板入站 %d (%s) -> 本地入站 %d：%d 个客户端\n", mapping.ID, inbound.ID, inbound.Remark, mapping.LocalInboundID, len(clients))
	}
	return nil
}
