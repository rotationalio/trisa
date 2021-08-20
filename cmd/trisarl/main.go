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
	app.Version = trisarl.Version()

	app.Commands = []*cli.Command{
		{
			Name:     "serve",
			Usage:    "run the TRISA server",
			Category: "server",
			Action:   serve,
			Flags:    []cli.Flag{},
		},
	}

	app.Run(os.Args)
}

func serve(c *cli.Context) (err error) {
	return nil
}
