package main

import (
	"os"

	"github.com/joho/godotenv"
	trisarl "github.com/rotationalio/trisa/pkg"
	"github.com/rotationalio/trisa/pkg/config"
	"github.com/urfave/cli/v2"
)

func main() {
	// Load the .env file if it exits
	godotenv.Load()

	// Instantiate the CLI application
	app := cli.NewApp()
	app.Name = "trisarl"
	app.Usage = "run the rotational trisa server"
	app.UsageText = "trisarl [-a]"
	app.Version = trisarl.Version()
	app.Action = serve
	app.Flags = []cli.Flag{
		&cli.StringFlag{
			Name:    "addr",
			Aliases: []string{"a"},
			Usage:   "the address and port to bind the server on",
			Value:   ":2384",
			EnvVars: []string{"TRISA_BIND_ADDR"},
		},
	}
	app.Commands = []*cli.Command{}

	app.Run(os.Args)
}

func serve(c *cli.Context) (err error) {
	var conf config.Config
	if conf, err = config.New(); err != nil {
		return cli.Exit(err, 1)
	}
	conf.BindAddr = c.String("addr")

	var srv *trisarl.Server
	if srv, err = trisarl.New(conf); err != nil {
		return cli.Exit(err, 1)
	}

	if err = srv.Serve(); err != nil {
		return cli.Exit(err, 1)
	}
	return nil
}
