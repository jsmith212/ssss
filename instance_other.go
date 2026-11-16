//go:build !linux

package main

import "github.com/wailsapp/wails/v3/pkg/application"

func singleInstance(onSecond func(application.SecondInstanceData)) *application.SingleInstanceOptions {
	return &application.SingleInstanceOptions{UniqueID: instanceID, OnSecondInstanceLaunch: onSecond}
}
