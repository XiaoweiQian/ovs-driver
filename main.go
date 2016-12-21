package main

import (
	"os"

	"github.com/Sirupsen/logrus"
	"github.com/XiaoweiQian/ovs-driver/drivers"
	"github.com/codegangsta/cli"
	pluginNet "github.com/docker/go-plugins-helpers/network"
)

const (
	version = "0.1"
)

func main() {

	var flagDebug = cli.BoolFlag{
		Name:  "debug, d",
		Usage: "enable debugging",
	}
	app := cli.NewApp()
	app.Name = "docker-ovs"
	app.Usage = "Docker Open vSwitch Networking"
	app.Version = version
	app.Flags = []cli.Flag{
		flagDebug,
	}
	app.Action = Run
	app.Run(os.Args)
}

// Run initializes the driver
func Run(ctx *cli.Context) {
	if ctx.Bool("debug") {
		logrus.SetLevel(logrus.DebugLevel)
	}

	d, err := drivers.NewDriver()
	if err != nil {
		panic(err)
	}
	h := pluginNet.NewHandler(d)
	h.ServeUnix("root", "ovs")
}
