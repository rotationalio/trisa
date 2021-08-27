package trisarl

import (
	"context"
	"crypto/rsa"
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
	"github.com/trisacrypto/trisa/pkg/trisa/mtls"
	"github.com/trisacrypto/trisa/pkg/trust"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

	// Create the server
	s = &Server{conf: conf, errc: make(chan error, 1)}

	// Attempt to load and parse the TRISA certificates for server-side TLS
	// Note that the signingKey is the same as the TRISA mTLS certificates for now
	var sz *trust.Serializer
	if sz, err = trust.NewSerializer(false); err != nil {
		return nil, err
	}

	// Read the certificates that were issued by the directory service
	if s.mtlsCerts, err = sz.ReadFile(conf.ServerCerts); err != nil {
		return nil, err
	}

	// Read the trust pool that was issued by the directory service (public CA keys)
	if s.trustPool, err = sz.ReadPoolFile(conf.ServerCertPool); err != nil {
		return nil, err
	}

	// Extract the signing key from the TRISA certificate
	if s.signingKey, err = s.mtlsCerts.GetRSAKeys(); err != nil {
		return nil, err
	}
	return s, nil
}

// Server implements the TRISAIntegration and TRISAHealth Services
type Server struct {
	protocol.UnimplementedTRISANetworkServer
	protocol.UnimplementedTRISAHealthServer
	conf       config.Config
	srv        *grpc.Server
	mtlsCerts  *trust.Provider
	trustPool  trust.ProviderPool
	signingKey *rsa.PrivateKey
	errc       chan error
}

// Serve TRISA requests.
func (s *Server) Serve() (err error) {
	// Create TLS Credentials for the server
	var creds grpc.ServerOption
	if creds, err = mtls.ServerCreds(s.mtlsCerts, s.trustPool); err != nil {
		return err
	}

	// Initialize the gRPC server
	s.srv = grpc.NewServer(creds)
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

func (s *Server) Transfer(ctx context.Context, in *protocol.SecureEnvelope) (out *protocol.SecureEnvelope, err error) {
	log.Info().Msg("unary transfer request received")
	return nil, status.Error(codes.Unimplemented, "still working on implementing Transfer")
}

func (s *Server) TransferStream(stream protocol.TRISANetwork_TransferStreamServer) (err error) {
	log.Info().Msg("transfer stream opened")
	return status.Error(codes.Unimplemented, "still working on implementing TransferStream")
}

func (s *Server) ConfirmAddress(ctx context.Context, in *protocol.Address) (out *protocol.AddressConfirmation, err error) {
	// TODO: return a gRPC error
	log.Info().Msg("confirm address")
	return nil, &protocol.Error{
		Code:    protocol.Unimplemented,
		Message: "Rotational Labs has not implemented address confirmation yet",
		Retry:   false,
	}
}

func (s *Server) KeyExchange(ctx context.Context, in *protocol.SigningKey) (out *protocol.SigningKey, err error) {
	log.Info().Msg("key exchange request received")
	return nil, status.Error(codes.Unimplemented, "still working on implementing KeyExchange")
}

func (s *Server) Status(ctx context.Context, in *protocol.HealthCheck) (out *protocol.ServiceState, err error) {
	log.Info().
		Uint32("attempts", in.Attempts).
		Str("last_checked_at", in.LastCheckedAt).
		Msg("status check")

	// Request another health check between 30 minutes and an hour from now.
	now := time.Now()
	out = &protocol.ServiceState{
		Status:    protocol.ServiceState_HEALTHY,
		NotBefore: now.Add(30 * time.Minute).Format(time.RFC3339),
		NotAfter:  now.Add(1 * time.Hour).Format(time.RFC3339),
	}

	// If we're in maintenance mode, change the service state appropriately
	if s.conf.Maintenance {
		out.Status = protocol.ServiceState_MAINTENANCE
	}

	return out, nil
}
