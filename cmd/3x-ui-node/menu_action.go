package main

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/mhsanaei/3x-ui/v3/internal/node"
)

func menuAction(choice, configPath string, c node.Config, reader *bufio.Reader, out io.Writer) error {
	var err error
	switch choice {
	case "1":
		err = showStatus(out, c)
	case "2":
		err = showInbounds(out, c)
	case "3":
		err = showClients(out, c)
	case "4":
		err = showPorts(out, c)
	case "5":
		err = showErrors(out, c)
	case "6":
		pin, pinErr := node.Fingerprint(c)
		if pinErr != nil {
			err = pinErr
			break
		}
		fmt.Fprintf(out, "\nAPI token: %s\nTLS SHA256: %s\nListen: %s\nBase path: %s\n", c.Token, pin, c.Listen, c.BasePath)
	case "7":
		err = serviceAction("start", out)
	case "8":
		var confirm string
		confirm, err = prompt(reader, out, "停止会中断节点连接，确认停止？y/n", "n")
		if err == nil && (strings.EqualFold(confirm, "y") || strings.EqualFold(confirm, "yes")) {
			err = serviceAction("stop", out)
		}
	case "9":
		err = serviceAction("restart", out)
	case "10":
		err = showLogs(c, out)
	case "11":
		showDefaultClient(out, c)
	case "12":
		err = configureDefaultClient(configPath, c, reader, out)
	case "13":
		err = addClientInteractive(c, reader, out)
	case "14":
		err = deleteClientInteractive(c, reader, out)
	case "0", "q", "Q":
		return nil
	default:
		return fmt.Errorf("无效选择")
	}
	return err
}
