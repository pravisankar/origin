package openshift

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"

	kapi "github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	log "github.com/Sirupsen/logrus"
	ctxu "github.com/docker/distribution/context"
	repoauth "github.com/docker/distribution/registry/auth"
	authorizationapi "github.com/openshift/origin/pkg/authorization/api"
	"github.com/openshift/origin/pkg/dockerregistry"
	"golang.org/x/net/context"
)

func init() {
	repoauth.Register("openshift", repoauth.InitFunc(newAccessController))
}

type AccessController struct {
	UserRegistryConfig *dockerregistry.UserRegistryConfig
}

type authChallenge struct {
	err error
}

type OpenShiftAccess struct {
	Namespace   string
	ImageRepo   string
	Verb        string
	BearerToken string
}

var _ repoauth.AccessController = &AccessController{}
var _ repoauth.Challenge = &authChallenge{}

// Errors used and exported by this package.
var (
	ErrTokenRequired          = errors.New("authorization header with basic token required")
	ErrTokenInvalid           = errors.New("failed to decode basic token")
	ErrOpenShiftTokenRequired = errors.New("expected openshift bearer token as password for basic token to registry")
	ErrNamespaceRequired      = errors.New("repository namespace required")
	ErrOpenShiftAccessDenied  = errors.New("openshift access denied")
)

func newAccessController(options map[string]interface{}) (repoauth.AccessController, error) {
	fmt.Println("Using OpenShift Auth handler")

	var rc dockerregistry.UserRegistryConfig
	err := rc.SetRegistryConfig()
	if err != nil {
		return nil, err
	}
	return &AccessController{
		UserRegistryConfig: &rc,
	}, nil
}

// Error returns the internal error string for this authChallenge.
func (ac *authChallenge) Error() string {
	return ac.err.Error()
}

// challengeParams constructs the value to be used in
// the WWW-Authenticate response challenge header.
// See https://tools.ietf.org/html/rfc6750#section-3
func (ac *authChallenge) challengeParams() string {
	return fmt.Sprintf("Basic realm=openshift error=%s", ac.Error())
}

// ServeHttp handles writing the challenge response
// by setting the challenge header and status code.
func (ac *authChallenge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("WWW-Authenticate", ac.challengeParams())
	w.WriteHeader(http.StatusUnauthorized)
}

// Authorized handles checking whether the given request is authorized
// for actions on resources allowed by openshift.
func (ac *AccessController) Authorized(ctx context.Context, accessRecords ...repoauth.Access) (context.Context, error) {
	req, err := ctxu.GetRequest(ctx)
	if err != nil {
		return nil, err
	}
	challenge := &authChallenge{}

	authParts := strings.SplitN(req.Header.Get("Authorization"), " ", 2)
	if len(authParts) != 2 || strings.ToLower(authParts[0]) != "basic" {
		challenge.err = ErrTokenRequired
		return nil, challenge
	}
	basicToken := authParts[1]

	bearerToken := ""
	for _, access := range accessRecords {
		log.Debugf("%s:%s:%s", access.Resource.Type, access.Resource.Name, access.Action)

		if access.Resource.Type != "repository" {
			continue
		}

		if len(bearerToken) == 0 {
			payload, err := base64.StdEncoding.DecodeString(basicToken)
			if err != nil {
				log.Errorf("Basic token decode failed: %s", err)
				challenge.err = ErrTokenInvalid
				return nil, challenge
			}
			authParts = strings.SplitN(string(payload), ":", 2)
			if len(authParts) != 2 {
				challenge.err = ErrOpenShiftTokenRequired
				return nil, challenge
			}
			bearerToken = authParts[1]
		}

		repoParts := strings.SplitN(access.Resource.Name, "/", 2)
		if len(repoParts) != 2 {
			challenge.err = ErrNamespaceRequired
			return nil, challenge
		}
		osAccess := &OpenShiftAccess{
			Namespace:   repoParts[0],
			ImageRepo:   repoParts[1],
			BearerToken: bearerToken,
		}

		switch access.Action {
		case "push":
			osAccess.Verb = "create"
		case "pull":
			osAccess.Verb = "get"
		default:
			challenge.err = fmt.Errorf("Unkown action: %s", access.Action)
			return nil, challenge
		}

		err = VerifyOpenShiftAccess(osAccess, ac)
		if err != nil {
			challenge.err = err
			return nil, challenge
		}
	}
	return context.WithValue(ctx, "BearerToken", bearerToken), nil
}

func VerifyOpenShiftAccess(osAccess *OpenShiftAccess, ac *AccessController) error {
	client, err := ac.UserRegistryConfig.GetRegistryClient(osAccess.BearerToken)
	if err != nil {
		return err
	}
	sar := authorizationapi.SubjectAccessReview{
		TypeMeta: kapi.TypeMeta{
			APIVersion: "v1beta1",
			Kind:       "SubjectAccessReview",
		},
		Verb:         osAccess.Verb,
		Resource:     "imageRepositories",
		ResourceName: osAccess.ImageRepo,
	}
	response, err := client.SubjectAccessReviews(osAccess.Namespace).Create(&sar)
	if err != nil {
		log.Errorf("OpenShift client error: %s", err)
		return ErrOpenShiftAccessDenied
	}
	if !response.Allowed {
		log.Errorf("OpenShift access denied: %s", response.Reason)
		return ErrOpenShiftAccessDenied
	}
	return nil
}
