package dockerregistry

import (
	"errors"
	"fmt"
	"os"

	kclient "github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	osclient "github.com/openshift/origin/pkg/client"
)

type UserRegistryConfig struct {
	config *kclient.Config
}

func (urc *UserRegistryConfig) SetRegistryConfig() error {
	config, err := getDefaultOpenShiftClientConfig()
	if err != nil {
		return err
	}
	urc.config = config
	return nil
}

func (urc *UserRegistryConfig) GetRegistryClient(bearerToken string) (*osclient.Client, error) {
	urc.config.BearerToken = bearerToken
	registryClient, err := osclient.New(urc.config)
	if err != nil {
		return nil, fmt.Errorf("Error creating OpenShift client: %s", err)
	}
	return registryClient, nil
}

type SystemRegistryConfig struct {
	config *kclient.Config
}

func (src *SystemRegistryConfig) GetRegistryClient() (*osclient.Client, error) {
	config, err := getDefaultOpenShiftClientConfig()
	if err != nil {
		return nil, err
	}
	var tlsClientConfig kclient.TLSClientConfig
	if !config.Insecure {
		certData := os.Getenv("OPENSHIFT_CERT_DATA")
		if len(certData) == 0 {
			return nil, errors.New("OPENSHIFT_CERT_DATA is required")
		}
		certKeyData := os.Getenv("OPENSHIFT_KEY_DATA")
		if len(certKeyData) == 0 {
			return nil, errors.New("OPENSHIFT_KEY_DATA is required")
		}
		tlsClientConfig = kclient.TLSClientConfig{
			CAData:   config.TLSClientConfig.CAData,
			CertData: []byte(certData),
			KeyData:  []byte(certKeyData),
		}
	}
	config.TLSClientConfig = tlsClientConfig
	registryClient, err := osclient.New(config)
	if err != nil {
		return nil, fmt.Errorf("Error creating OpenShift client: %s", err)
	}
	return registryClient, nil
}

func getDefaultOpenShiftClientConfig() (*kclient.Config, error) {
	openshiftAddr := os.Getenv("OPENSHIFT_MASTER")
	if len(openshiftAddr) == 0 {
		return nil, errors.New("OPENSHIFT_MASTER is required")
	}

	insecure := os.Getenv("OPENSHIFT_INSECURE") == "true"
	var tlsClientConfig kclient.TLSClientConfig
	if !insecure {
		caData := os.Getenv("OPENSHIFT_CA_DATA")
		if len(caData) == 0 {
			return nil, errors.New("OPENSHIFT_CA_DATA is required")
		}
		tlsClientConfig = kclient.TLSClientConfig{
			CAData: []byte(caData),
		}
	}

	return &kclient.Config{
		Host:            openshiftAddr,
		TLSClientConfig: tlsClientConfig,
		Insecure:        insecure,
	}, nil
}
