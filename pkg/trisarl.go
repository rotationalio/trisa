package trisarl

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"time"

	"github.com/rotationalio/trisa/pkg/config"
	"github.com/rotationalio/trisa/pkg/logger"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/trisacrypto/trisa/pkg/ivms101"
	protocol "github.com/trisacrypto/trisa/pkg/trisa/api/v1beta1"
	generic "github.com/trisacrypto/trisa/pkg/trisa/data/generic/v1beta1"
	"github.com/trisacrypto/trisa/pkg/trisa/handler"
	"github.com/trisacrypto/trisa/pkg/trisa/mtls"
	"github.com/trisacrypto/trisa/pkg/trisa/peers"
	"github.com/trisacrypto/trisa/pkg/trust"
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

	// Manage remote peers using the same credentials as the server
	s.peers = peers.New(s.mtlsCerts, s.trustPool, s.conf.DirectoryAddr)
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
	peers      *peers.Peers
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
	// Get the peer from the context
	var peer *peers.Peer
	if peer, err = s.peers.FromContext(ctx); err != nil {
		log.Error().Err(err).Msg("could not verify peer from incoming request")
		return nil, &protocol.Error{
			Code:    protocol.Unverified,
			Message: err.Error(),
		}
	}
	log.Info().Str("peer", peer.String()).Str("id", in.Id).Msg("unary transfer request received")

	// Ensure peer signing key is available to send a response
	if peer.SigningKey() == nil {
		log.Warn().Str("peer", peer.String()).Msg("no signing key available")
		return nil, &protocol.Error{
			Code:    protocol.NoSigningKey,
			Message: "please retry transfer after key exchange",
			Retry:   true,
		}
	}

	return s.handleTransaction(ctx, peer, in)
}

func (s *Server) TransferStream(stream protocol.TRISANetwork_TransferStreamServer) (err error) {
	var peer *peers.Peer
	ctx := stream.Context()
	if peer, err = s.peers.FromContext(ctx); err != nil {
		log.Error().Err(err).Msg("could not verify peer from incoming stream")
		return &protocol.Error{
			Code:    protocol.Unverified,
			Message: err.Error(),
		}
	}
	log.Info().Str("peer", peer.String()).Msg("transfer stream opened")

	// Ensure peer signing key is available to send a response
	if peer.SigningKey() == nil {
		log.Warn().Str("peer", peer.String()).Msg("no signing key available")
		return &protocol.Error{
			Code:    protocol.NoSigningKey,
			Message: "please retry transfer stream after key exchange",
			Retry:   true,
		}
	}

	// Handle incoming secure envelopes from client
	var nmessages uint64
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		var in *protocol.SecureEnvelope
		if in, err = stream.Recv(); err != nil {
			if err == io.EOF {
				log.Info().
					Str("peer", peer.String()).
					Uint64("total_messages", nmessages).
					Msg("transfer stream closed")
				return nil
			}
			log.Warn().Err(err).Msg("transfer stream recv error")
			return protocol.Errorf(protocol.Unavailable, "stream closed prematurely: %s", err)
		}

		// Handle the response
		nmessages++
		var out *protocol.SecureEnvelope
		if out, err = s.handleTransaction(ctx, peer, in); err != nil {
			// Do not close the stream for TRISA coded errors, send the error in the secure envelope
			switch trisaErr := err.(type) {
			case *protocol.Error:
				out = &protocol.SecureEnvelope{Error: trisaErr}
			default:
				return err
			}
		}

		// Send the response
		if err = stream.Send(out); err != nil {
			log.Error().Err(err).Msg("transfer stream send error")
			return protocol.Errorf(protocol.Unavailable, "stream closed prematurely: %s", err)
		}

		// Log the message
		log.Info().Str("peer", peer.String()).Str("id", in.Id).Uint64("n_messages", nmessages).Msg("streaming transfer request received")
	}
}

// Although the Rotational Server does not do Transfers, it still attempts to decode
// the message in order to send back correct TRISA errors if the message is incorrect
// for any reason, then it simply sends a NO_COMPLIANCE error at the end.
func (s *Server) handleTransaction(ctx context.Context, peer *peers.Peer, in *protocol.SecureEnvelope) (out *protocol.SecureEnvelope, err error) {
	// Decrypt the encryption key and HMAC secret with private signing keys (asymmetric phase)
	// Note that the handler.Open function will return a TRISA protocol error.
	var envelope *handler.Envelope
	if envelope, err = handler.Open(in, s.signingKey); err != nil {
		log.Error().Err(err).Msg("could not open secure envelope")
		return nil, err
	}

	payload := envelope.Payload
	if payload.Identity.TypeUrl != "type.googleapis.com/ivms101.IdentityPayload" {
		log.Warn().Str("type", payload.Identity.TypeUrl).Msg("unsupported identity type")
		return nil, protocol.Errorf(protocol.UnparseableIdentity, "ivms101.IdentityPayload payload identity type required")
	}

	if payload.Transaction.TypeUrl != "type.googleapis.com/trisa.data.generic.v1beta1.Transaction" {
		log.Warn().Str("type", payload.Transaction.TypeUrl).Msg("unsupported transaction type")
		return nil, protocol.Errorf(protocol.UnparseableTransaction, "trisa.data.generic.v1beta1.Transaction payload transaction type required")
	}

	identity := &ivms101.IdentityPayload{}
	transaction := &generic.Transaction{}

	if err = payload.Identity.UnmarshalTo(identity); err != nil {
		log.Error().Err(err).Msg("could not unmarshal identity")
		return nil, protocol.Errorf(protocol.UnparseableIdentity, "could not unmarshal identity")
	}
	if err = payload.Transaction.UnmarshalTo(transaction); err != nil {
		log.Error().Err(err).Msg("could not unmarshal transaction")
		return nil, protocol.Errorf(protocol.UnparseableTransaction, "could not unmarshal transaction")
	}

	// Here is the point where you would start to handle the incoming request and return
	// the beneficiary information, loaded up from your database. Rotational Labs is not
	// a VASP though, so it returns a no compliance error.
	return nil, &protocol.Error{
		Code:    protocol.NoCompliance,
		Message: "Rotational Labs is not a VASP and therefore cannot perform Travel Rule compliance",
		Retry:   false,
	}
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
	// Get the peer from the context
	var peer *peers.Peer
	if peer, err = s.peers.FromContext(ctx); err != nil {
		log.Error().Err(err).Msg("could not verify peer from incoming request")
		return nil, &protocol.Error{
			Code:    protocol.Unverified,
			Message: err.Error(),
		}
	}
	log.Info().Str("peer", peer.String()).Msg("key exchange request received")

	// Cache key in the peers mapping
	// TODO: parse PEM data in addition to PKIX public key data
	var pub interface{}
	if pub, err = x509.ParsePKIXPublicKey(in.Data); err != nil {
		log.Error().Err(err).Int64("version", in.Version).Str("algorithm", in.PublicKeyAlgorithm).Msg("could not parse incoming PKIX public key")
		return nil, protocol.Errorf(protocol.NoSigningKey, "could not parse signing key")
	}

	if err = peer.UpdateSigningKey(pub); err != nil {
		log.Error().Err(err).Msg("could not update signing key")
		return nil, protocol.Errorf(protocol.UnhandledAlgorithm, "unsuported signing algorithm")
	}

	// Return the public signing-key of the service
	var key *x509.Certificate
	if key, err = s.mtlsCerts.GetLeafCertificate(); err != nil {
		log.Error().Err(err).Msg("could not extract leaf certificate")
		return nil, protocol.Errorf(protocol.InternalError, "could not return signing keys")
	}

	out = &protocol.SigningKey{
		Version:            int64(key.Version),
		Signature:          key.Signature,
		SignatureAlgorithm: key.SignatureAlgorithm.String(),
		PublicKeyAlgorithm: key.PublicKeyAlgorithm.String(),
		NotBefore:          key.NotBefore.Format(time.RFC3339),
		NotAfter:           key.NotAfter.Format(time.RFC3339),
	}

	if out.Data, err = x509.MarshalPKIXPublicKey(key.PublicKey); err != nil {
		log.Error().Err(err).Msg("could not marshal PKIX public key")
		return nil, protocol.Errorf(protocol.InternalError, "could not marshal public key")
	}
	return out, nil
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
