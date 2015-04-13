package cache

import (
	"fmt"

	kapi "github.com/GoogleCloudPlatform/kubernetes/pkg/api"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/client/cache"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/fields"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/labels"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/runtime"
	"github.com/GoogleCloudPlatform/kubernetes/pkg/watch"
)

type ProjectCache struct {
	Client              client.Interface
	Store               cache.Store
	DefaultNodeSelector string
}

var pcache *ProjectCache

func (p *ProjectCache) GetNamespaceObject(name string) (*kapi.Namespace, error) {
	// check for namespace in the cache
	namespaceObj, exists, err := p.Store.Get(&kapi.Namespace{
		ObjectMeta: kapi.ObjectMeta{
			Name:      name,
			Namespace: "",
		},
		Status: kapi.NamespaceStatus{},
	})
	if err != nil {
		return nil, err
	}

	var namespace *kapi.Namespace
	if exists {
		namespace = namespaceObj.(*kapi.Namespace)
	} else {
		// Our watch maybe latent, so we make a best effort to get the object, and only fail if not found
		namespace, err = p.Client.Namespaces().Get(name)
		// the namespace does not exit, so prevent create and update in that namespace
		if err != nil {
			return nil, fmt.Errorf("Namespace %s does not exist", name)
		}
	}
	return namespace, nil
}

func (p *ProjectCache) GetNodeSelector(namespace *kapi.Namespace) (labels.Selector, error) {
	selector := ""
	if len(namespace.ObjectMeta.Annotations) > 0 {
		if ns, ok := namespace.ObjectMeta.Annotations["nodeSelector"]; ok {
			selector = ns
		}
	}
	if len(selector) == 0 {
		if len(p.DefaultNodeSelector) == 0 {
			return labels.Everything(), nil
		} else {
			selectorObj, err := labels.Parse(p.DefaultNodeSelector)
			if err != nil {
				return nil, err
			}
			return selectorObj, nil
		}
	} else {
		selectorObj, err := labels.Parse(selector)
		if err != nil {
			return nil, err
		}
		return selectorObj, nil
	}
}

func GetProjectCache() (*ProjectCache, error) {
	if pcache == nil {
		return nil, fmt.Errorf("project cache not initialized")
	}
	return pcache, nil
}

func RunProjectCache(c client.Interface, defaultNodeSelector string) {
	store := cache.NewStore(cache.MetaNamespaceKeyFunc)
	reflector := cache.NewReflector(
		&cache.ListWatch{
			ListFunc: func() (runtime.Object, error) {
				return c.Namespaces().List(labels.Everything(), fields.Everything())
			},
			WatchFunc: func(resourceVersion string) (watch.Interface, error) {
				return c.Namespaces().Watch(labels.Everything(), fields.Everything(), resourceVersion)
			},
		},
		&kapi.Namespace{},
		store,
		0,
	)
	reflector.Run()
	pcache = &ProjectCache{
		Client:              c,
		Store:               store,
		DefaultNodeSelector: defaultNodeSelector,
	}
}
