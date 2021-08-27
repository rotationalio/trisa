package trisarl

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"time"

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
func New() (s *Server, err error) {
	s = &Server{errc: make(chan error, 1)}
	return s, nil
}

// Server implements the TRISAIntegration and TRISAHealth Services
type Server struct {
	protocol.UnimplementedTRISANetworkServer
	protocol.UnimplementedTRISAHealthServer
	srv  *grpc.Server
	errc chan error
}

// Serve TRISA requests.
func (s *Server) Serve(addr string) (err error) {
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
	if sock, err = net.Listen("tcp", addr); err != nil {
		return fmt.Errorf("could not listen on %q", addr)
	}
	defer sock.Close()

	// Run the server and handle requests
	go func() {
		log.Info().Str("listen", addr).Str("version", Version()).Msg("server started")
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
