package main

import (
	"github.com/godbus/dbus/v5"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// singleInstance returns the single-instance options, or nil when there is
// no D-Bus session bus to coordinate over (a bare X or Wayland session, a
// container): Wails treats an unreachable bus as fatal, and not starting at
// all is worse than starting unguarded.
func singleInstance(onSecond func(application.SecondInstanceData)) *application.SingleInstanceOptions {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil
	}
	conn.Close()
	return &application.SingleInstanceOptions{UniqueID: instanceID, OnSecondInstanceLaunch: onSecond}
}
