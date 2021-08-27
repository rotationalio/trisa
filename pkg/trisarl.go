package trisarl

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"time"

	"github.com/rotationalio/trisa/pkg/config"
	"github.com/rotationalio/trisa/pkg/logger"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	protocol "github.com/trisacrypto/trisa/pkg/trisa/api/v1beta1"
	"google.golang.org/grpc"
)

func init() {
	// Initialize zerolog with GCP logging requirements
	zerolog.TimeFieldFormat = time.RFC3339
	zerolog.TimestampFieldName = logger.GCPFieldKeyTime
	zerolog.MessageFieldName = logger.GCPFieldKeyMsg

	// Add the severity hook for GCP logging
	var gcpHook logger.SeverityHook
	log.Logger = zerolog.New(os.Stdout).Hook(gcpHook).With().Timestamp().Logger()
}

// New creates a new Rotational TRISA Server with the specified configuration and
// prepares it to listen for and respond to gRPC requests on the TRISA network.
func New(conf config.Config) (s *Server, err error) {
	// Load default configuration from the environment
	if conf.IsZero() {
		if conf, err = config.New(); err != nil {
			return nil, err
		}
	}

	// Set the global log level
	zerolog.SetGlobalLevel(zerolog.Level(conf.LogLevel))

	// Set human readable logging if console log is requested
	if conf.ConsoleLog {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	}

	s = &Server{conf: conf, errc: make(chan error, 1)}
	return s, nil
}

// Server implements the TRISAIntegration and TRISAHealth Services
type Server struct {
	protocol.UnimplementedTRISANetworkServer
	protocol.UnimplementedTRISAHealthServer
	conf config.Config
	srv  *grpc.Server
	errc chan error
}

// Serve TRISA requests.
func (s *Server) Serve() (err error) {
	// Initialize the gRPC server
	s.srv = grpc.NewServer()
	protocol.RegisterTRISANetworkServer(s.srv, s)
	protocol.RegisterTRISAHealthServer(s.srv, s)

	// Catch OS signals to ensure graceful shutdowns occur
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt)
	go func() {
		<-quit
		s.errc <- s.Shutdown()
	}()

	// Listen for TRISA service requests on the configured bind address and port
	var sock net.Listener
	if sock, err = net.Listen("tcp", s.conf.BindAddr); err != nil {
		return fmt.Errorf("could not listen on %q", s.conf.BindAddr)
	}
	defer sock.Close()

	// Run the server and handle requests
	go func() {
		log.Info().Str("listen", s.conf.BindAddr).Str("version", Version()).Msg("server started")
		if err := s.srv.Serve(sock); err != nil {
			s.errc <- err
		}
	}()

	// Listen for any errors and wait for all go routines to finish.
	if err = <-s.errc; err != nil {
		return err
	}
	return nil
}

// Shutdown the gRPC server gracefully.
func (s *Server) Shutdown() (err error) {
	log.Info().Msg("gracefully shutting down")
	s.srv.GracefulStop()
	log.Debug().Msg("successful shut down")
	return nil
}
