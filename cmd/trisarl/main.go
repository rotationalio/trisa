package main

import (
	"os"

	"github.com/joho/godotenv"
	trisarl "github.com/rotationalio/trisa/pkg"
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
	var srv *trisarl.Server
	if srv, err = trisarl.New(); err != nil {
		return cli.Exit(err, 1)
	}

	if err = srv.Serve(c.String("addr")); err != nil {
		return cli.Exit(err, 1)
	}
	return nil
}
