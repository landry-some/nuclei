package signer

import (
	"errors"
)

const (
	PrivateKeyEnvVarName = "NUCLEI_SIGNATURE_PRIVATE_KEY"
	PublicKeyEnvVarName  = "NUCLEI_SIGNATURE_PUBLIC_KEY"
	AlgorithmEnvVarName  = "NUCLEI_SIGNATURE_ALGORITHM"
)

var DefaultVerifiers []*Signer

func init() {
	// add default pd verifier
	if verifier, err := NewVerifier(&Options{PublicKeyData: pdPublicKey, Algorithm: RSA}); err == nil {
		DefaultVerifiers = append(DefaultVerifiers, verifier)
	}
}

func AddToDefault(s *Signer) error {
	if s == nil {
		return errors.New("signer is nil")
	}

	DefaultVerifiers = append(DefaultVerifiers, s)
	return nil
}
