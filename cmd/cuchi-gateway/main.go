package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/NetherQuartz/cu-chi/internal/gateway"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "Cuchi gateway:", err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) != 2 {
		return fmt.Errorf("expected init or prepare")
	}
	switch os.Args[1] {
	case "init":
		// Stage an entire identity, then atomically rename. Refuse to replace an
		// existing identity: changing VPSs must not rotate the router's WG keys.
		if _, err := os.Lstat("/config/lan"); !os.IsNotExist(err) {
			return fmt.Errorf("LAN identity already exists or cannot be inspected; preserve it when updating uplink.conf")
		}
		lan, err := gateway.NewLAN(os.Getenv("GATEWAY_LAN_IP"), os.Getenv("GATEWAY_ROUTER_IP"), os.Getenv("GATEWAY_CLIENT_CIDRS"))
		if err != nil {
			return err
		}
		stage, err := os.MkdirTemp("/config", ".lan-")
		if err != nil {
			return fmt.Errorf("cannot create private LAN identity directory")
		}
		defer os.RemoveAll(stage)
		for name, data := range map[string]string{"wg0.conf": lan.ServerConfig, "keenetic.conf": lan.RouterConfig, "router-ip": os.Getenv("GATEWAY_ROUTER_IP")} {
			if err := os.WriteFile(filepath.Join(stage, name), []byte(data), 0600); err != nil {
				return fmt.Errorf("cannot write LAN identity")
			}
		}
		if err := os.Rename(stage, "/config/lan"); err != nil {
			return fmt.Errorf("cannot publish LAN identity")
		}
		fmt.Println("Cuchi gateway LAN identity created; import /config/lan/keenetic.conf into Keenetic once. Keep this directory private.")
	case "prepare":
		data, err := os.ReadFile("/config/uplink.conf")
		if err != nil {
			return fmt.Errorf("cannot read /config/uplink.conf")
		}
		uplink, err := gateway.ParseUplink(string(data))
		if err != nil {
			return err
		}
		for name, data := range map[string]string{"uplink.conf": uplink.Config, "uplink-address": uplink.Address} {
			if err := os.WriteFile(filepath.Join("/run", name), []byte(data), 0600); err != nil {
				return fmt.Errorf("cannot prepare uplink")
			}
		}
	default:
		return fmt.Errorf("expected init or prepare")
	}
	return nil
}
