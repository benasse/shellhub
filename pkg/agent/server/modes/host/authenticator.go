package host

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"

	gliderssh "github.com/gliderlabs/ssh"
	"github.com/shellhub-io/shellhub/pkg/agent/pkg/osauth"
	"github.com/shellhub-io/shellhub/pkg/agent/server/modes"
	"github.com/shellhub-io/shellhub/pkg/api/client"
	"github.com/shellhub-io/shellhub/pkg/models"
	log "github.com/sirupsen/logrus"
	gossh "golang.org/x/crypto/ssh"
)

// NOTICE: Ensures the Authenticator interface is implemented.
var _ modes.Authenticator = (*Authenticator)(nil)

// Authenticator implements the Authenticator interface when the server is running in host mode.
type Authenticator struct {
	// api is a client to communicate with the ShellHub's API.
	api client.Client
	// authData is the authentication data received from the API to authenticate the device.
	authData *models.DeviceAuthResponse
	// singleUserPassword is the password of the single user.
	// When it is empty, it means that the single user is disabled.
	singleUserPassword string
	// deviceName is the device name.
	//
	// NOTICE: Uses a pointer for later assignment.
	deviceName *string
	// osauth is an instance of the OSAuth interface to authenticate the user on the Operating System.
	osauth osauth.OSAuther
}

// NewAuthenticator creates a new instance of Authenticator for the host mode.
// It receives the api client to perform requests to the ShellHub's API, the authentication data received by the agent
// when started the communication between it and the agent, the singleUserPassword, what indicates is is running at
// this mode and the deviceName.
//
// The deviceName is a pointer to a string because when the server is created, we don't know the device name yet, that
// is set later.
func NewAuthenticator(api client.Client, authData *models.DeviceAuthResponse, singleUserPassword string, deviceName *string) *Authenticator {
	return &Authenticator{
		api:                api,
		authData:           authData,
		singleUserPassword: singleUserPassword,
		deviceName:         deviceName,
		osauth:             new(osauth.OSAuth),
	}
}

// Password handles the server's SSH password authentication when server is running in host mode.
func (a *Authenticator) Password(ctx gliderssh.Context, _ string, pass string) bool {
	log := log.WithFields(log.Fields{
		"user": ctx.User(),
	})
	var ok bool

	if a.singleUserPassword == "" {
		ok = a.osauth.AuthUser(ctx.User(), pass)
	} else {
		ok = a.osauth.VerifyPasswordHash(a.singleUserPassword, pass)
	}

	if ok {
		log.Info("Accepted password")
	} else {
		log.Info("Failed password")
	}

	return ok
}

// PublicKey handles the server's SSH public key authentication when server is running in host mode.
func (a *Authenticator) PublicKey(ctx gliderssh.Context, _ string, key gliderssh.PublicKey) bool {
	if a.osauth.LookupUser(ctx.User()) == nil {
		return false
	}

	type Signature struct {
		Username  string
		Namespace string
	}

	sig := &Signature{
		Username:  ctx.User(),
		Namespace: *a.deviceName,
	}

	sigBytes, err := json.Marshal(sig)
	if err != nil {
		return false
	}

	sigHash := sha256.Sum256(sigBytes)

	res, err := a.api.AuthPublicKey(&models.PublicKeyAuthRequest{
		Fingerprint: gossh.FingerprintLegacyMD5(key),
		Data:        string(sigBytes),
	}, a.authData.Token)
	if err != nil {
		return false
	}

	digest, err := base64.StdEncoding.DecodeString(res.Signature)
	if err != nil {
		return false
	}

	cryptoKey, ok := key.(gossh.CryptoPublicKey)
	if !ok {
		return false
	}

	pubCrypto := cryptoKey.CryptoPublicKey()

	pubKey, ok := pubCrypto.(*rsa.PublicKey)
	if !ok {
		return false
	}

	if err = rsa.VerifyPKCS1v15(pubKey, crypto.SHA256, sigHash[:], digest); err != nil {
		return false
	}

	return true
}
