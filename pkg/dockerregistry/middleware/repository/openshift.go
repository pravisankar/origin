package repository

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	kapi "github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	kerrors "github.com/GoogleCloudPlatform/kubernetes/pkg/api/errors"
	log "github.com/Sirupsen/logrus"
	"github.com/docker/distribution"
	"github.com/docker/distribution/digest"
	"github.com/docker/distribution/manifest"
	repomw "github.com/docker/distribution/registry/middleware/repository"
	"github.com/docker/libtrust"
	"github.com/openshift/origin/pkg/client"
	"github.com/openshift/origin/pkg/dockerregistry"
	imageapi "github.com/openshift/origin/pkg/image/api"
	"golang.org/x/net/context"
)

func init() {
	repomw.Register("openshift", repomw.InitFunc(newRepository))
}

type repository struct {
	distribution.Repository

	sysregClient *client.Client
	usrregConfig *dockerregistry.UserRegistryConfig
	registryAddr string
	namespace    string
	name         string
}

// newRepository returns a new repository middleware.
func newRepository(repo distribution.Repository, options map[string]interface{}) (distribution.Repository, error) {

	var urc dockerregistry.UserRegistryConfig
	err := urc.SetRegistryConfig()
	if err != nil {
		return nil, err
	}
	var src dockerregistry.SystemRegistryConfig
	sysregClient, err := src.GetRegistryClient()
	if err != nil {
		return nil, err
	}
	registryAddr := os.Getenv("REGISTRY_URL")
	if len(registryAddr) == 0 {
		return nil, errors.New("REGISTRY_URL is required")
	}

	nameParts := strings.SplitN(repo.Name(), "/", 2)
	if len(nameParts) != 2 {
		return nil, errors.New("Incorrect image repo name")
	}
	return &repository{
		Repository:   repo,
		sysregClient: sysregClient,
		usrregConfig: &urc,
		registryAddr: registryAddr,
		namespace:    nameParts[0],
		name:         nameParts[1],
	}, nil
}

// Manifests returns r, which implements distribution.ManifestService.
func (r *repository) Manifests() distribution.ManifestService {
	return r
}

// Tags lists the tags under the named repository.
func (r *repository) Tags(ctx context.Context) ([]string, error) {
	imageRepository, err := r.getImageRepository(ctx)
	if err != nil {
		return []string{}, nil
	}
	tags := []string{}
	for tag := range imageRepository.Status.Tags {
		tags = append(tags, tag)
	}

	return tags, nil
}

// Exists returns true if the manifest specified by dgst exists.
func (r *repository) Exists(ctx context.Context, dgst digest.Digest) (bool, error) {
	image, err := r.getImage(ctx, dgst)
	if err != nil {
		return false, err
	}
	return image != nil, nil
}

// ExistsByTag returns true if the manifest with tag `tag` exists.
func (r *repository) ExistsByTag(ctx context.Context, tag string) (bool, error) {
	imageRepository, err := r.getImageRepository(ctx)
	if err != nil {
		return false, err
	}
	_, found := imageRepository.Status.Tags[tag]
	return found, nil
}

// Get retrieves the manifest with digest `dgst`.
func (r *repository) Get(ctx context.Context, dgst digest.Digest) (*manifest.SignedManifest, error) {
	_, err := r.getImageStreamImage(ctx, dgst)
	if err != nil {
		return nil, err
	}

	image, err := r.getImage(ctx, dgst)
	if err != nil {
		return nil, err
	}

	return r.manifestFromImage(image)
}

// Get retrieves the named manifest, if it exists.
func (r *repository) GetByTag(ctx context.Context, tag string) (*manifest.SignedManifest, error) {
	image, err := r.getImageRepositoryTag(ctx, tag)
	if err != nil {
		// TODO remove when docker 1.6 is out
		// Since docker 1.5 doesn't support pull by id, we're simulating pull by id
		// against the v2 registry by using a pull spec of the form
		// <repo>:<hex portion of digest>, so once we verify we got a 404 from
		// getImageRepositoryTag, we construct a digest and attempt to get the
		// imageStreamImage using that digest.

		// TODO replace with kerrors.IsStatusError when it's rebased in
		if err, ok := err.(*kerrors.StatusError); !ok {
			log.Errorf("GetByTag: getImageRepositoryTag returned error: %s", err)
			return nil, err
		} else if err.ErrStatus.Code != http.StatusNotFound {
			log.Errorf("GetByTag: getImageRepositoryTag returned non-404: %s", err)
		}
		// let's try to get by id
		dgst, dgstErr := digest.ParseDigest("sha256:" + tag)
		if dgstErr != nil {
			log.Errorf("GetByTag: unable to parse digest: %s", dgstErr)
			return nil, err
		}
		image, err = r.getImageStreamImage(ctx, dgst)
		if err != nil {
			log.Errorf("GetByTag: getImageStreamImage returned error: %s", err)
			return nil, err
		}
	}

	dgst, err := digest.ParseDigest(image.Name)
	if err != nil {
		return nil, err
	}

	image, err = r.getImage(ctx, dgst)
	if err != nil {
		return nil, err
	}

	return r.manifestFromImage(image)
}

// Put creates or updates the named manifest.
func (r *repository) Put(ctx context.Context, manifest *manifest.SignedManifest) error {
	// Resolve the payload in the manifest.
	payload, err := manifest.Payload()
	if err != nil {
		return err
	}

	// Calculate digest
	dgst, err := digest.FromBytes(payload)
	if err != nil {
		return err
	}

	// Upload to openshift
	irm := imageapi.ImageRepositoryMapping{
		TypeMeta: kapi.TypeMeta{
			APIVersion: "v1beta1",
			Kind:       "ImageRepositoryMapping",
		},
		ObjectMeta: kapi.ObjectMeta{
			Namespace: r.namespace,
			Name:      r.name,
		},
		Tag: manifest.Tag,
		Image: imageapi.Image{
			ObjectMeta: kapi.ObjectMeta{
				Name: dgst.String(),
			},
			DockerImageReference: fmt.Sprintf("%s/%s/%s@%s", r.registryAddr, r.namespace, r.name, dgst.String()),
			DockerImageManifest:  string(payload),
		},
	}

	if err := r.sysregClient.ImageRepositoryMappings(r.namespace).Create(&irm); err != nil {
		log.Errorf("Error creating ImageRepositoryMapping: %s", err)
		return err
	}

	// Grab each json signature and store them.
	signatures, err := manifest.Signatures()
	if err != nil {
		return err
	}

	for _, signature := range signatures {
		if err := r.Signatures().Put(dgst, signature); err != nil {
			log.Errorf("Error storing signature: %s", err)
			return err
		}
	}

	return nil
}

// Delete deletes the manifest with digest `dgst`.
func (r *repository) Delete(ctx context.Context, dgst digest.Digest) error {
	return r.sysregClient.Images().Delete(dgst.String())
}

// getImageRepository retrieves the ImageRepository for r.
func (r *repository) getImageRepository(ctx context.Context) (*imageapi.ImageRepository, error) {
	client, err := r.usrregConfig.GetRegistryClient(ctx.Value("BearerToken").(string))
	if err != nil {
		return nil, err
	}
	return client.ImageRepositories(r.namespace).Get(r.name)
}

// getImage retrieves the Image with digest `dgst`. This uses the registry's
// credentials and should ONLY
func (r *repository) getImage(ctx context.Context, dgst digest.Digest) (*imageapi.Image, error) {
	return r.sysregClient.Images().Get(dgst.String())
}

// getImageRepositoryTag retrieves the Image with tag `tag` for the ImageRepository
// associated with r.
func (r *repository) getImageRepositoryTag(ctx context.Context, tag string) (*imageapi.Image, error) {
	client, err := r.usrregConfig.GetRegistryClient(ctx.Value("BearerToken").(string))
	if err != nil {
		return nil, err
	}
	return client.ImageRepositoryTags(r.namespace).Get(r.name, tag)
}

// getImageStreamImage retrieves the Image with digest `dgst` for the ImageRepository
// associated with r. This ensures the user has access to the image.
func (r *repository) getImageStreamImage(ctx context.Context, dgst digest.Digest) (*imageapi.Image, error) {
	client, err := r.usrregConfig.GetRegistryClient(ctx.Value("BearerToken").(string))
	if err != nil {
		return nil, err
	}
	return client.ImageStreamImages(r.namespace).Get(r.name, dgst.String())
}

// manifestFromImage converts an Image to a SignedManifest.
func (r *repository) manifestFromImage(image *imageapi.Image) (*manifest.SignedManifest, error) {
	dgst, err := digest.ParseDigest(image.Name)
	if err != nil {
		return nil, err
	}

	// Fetch the signatures for the manifest
	signatures, err := r.Signatures().Get(dgst)
	if err != nil {
		return nil, err
	}

	jsig, err := libtrust.NewJSONSignature([]byte(image.DockerImageManifest), signatures...)
	if err != nil {
		return nil, err
	}

	// Extract the pretty JWS
	raw, err := jsig.PrettySignature("signatures")
	if err != nil {
		return nil, err
	}

	var sm manifest.SignedManifest
	if err := json.Unmarshal(raw, &sm); err != nil {
		return nil, err
	}
	return &sm, err
}
