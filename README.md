# TRISA-RL

**Rotational Labs TRISA Service Implementation**

This is a Golang implementation of a TRISA server that handles incoming TRISA requests (no outgoing requests). Rotational Labs is not a VASP, therefore this implementation mostly serves as a sketch for how to quickly implement a TRISA server in Go and is also used as a testing backstop for Rotational Labs partners who would like to ensure their TRISA messages are being parsed correctly (though Rotational Labs recommends using the [rVASPs](https://trisa.dev/testnet/rvasps/) for this purpose).

For more information on TRISA, please visit [trisa.io](https://trisa.io).

## Quickstart

Extract the certs that you received from the Directory Service:

    $ unzip 250000.zip
    $ openssl pkcs12 -in trisa.example.com.p12 -out trisa.example.com.pem -nodes

Edit the `.env` file and make sure that the `$TRISA_SERVER_CERTS` and `$TRISA_SERVER_CERTPOOL` environment variables are set to the path of `trisa.example.com.pem`.

Edit `/etc/hosts` to map your localhost to the common name of the certs (otherwise the TLS connection will fail).

Run the TRISA Server in development mode:

    $ go run ./cmd/trisarl

## Deploying

Build the Docker image locally:

    $ docker build -t rotationalio/trisarl .

Run the Docker image:

    $ docker run -p 443:443 -v $PWD/fixtures:/app \
        --env TRISA_SERVER_CERTS=/app/trisa.rotational.io.pem \
        --env TRISA_SERVER_CERTPOOL=/app/trisa.rotational.io.pem \
        rotationalio/trisarl

