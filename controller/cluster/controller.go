package cluster

import (
	"fmt"
	"sync"
	"time"

	"github.com/golang/glog"
	"github.com/kubermatic/api"
	"github.com/kubermatic/api/controller"
	"github.com/kubermatic/api/provider"
	kprovider "github.com/kubermatic/api/provider/kubernetes"
	kapi "k8s.io/kubernetes/pkg/api"
	kerrors "k8s.io/kubernetes/pkg/api/errors"
	"k8s.io/kubernetes/pkg/client/cache"
	"k8s.io/kubernetes/pkg/client/record"
	client "k8s.io/kubernetes/pkg/client/unversioned"
	kcontroller "k8s.io/kubernetes/pkg/controller"
	"k8s.io/kubernetes/pkg/controller/framework"
	"k8s.io/kubernetes/pkg/fields"
	"k8s.io/kubernetes/pkg/labels"
	"k8s.io/kubernetes/pkg/runtime"
	"k8s.io/kubernetes/pkg/types"
	"k8s.io/kubernetes/pkg/util"
	"k8s.io/kubernetes/pkg/util/workqueue"
	"k8s.io/kubernetes/pkg/watch"
)

const (
	fullResyncPeriod               = 5 * time.Minute
	namespaceStoreSyncedPollPeriod = 100 * time.Millisecond
	workerNum                      = 5
	maxUpdateRetries               = 5
	launchTimeout                  = 5 * time.Minute

	workerPeriod      = time.Second
	pendingSyncPeriod = 10 * time.Second
	runningSyncPeriod = 1 * time.Minute
)

type clusterController struct {
	dc                  string
	client              *client.Client
	queue               *workqueue.Type // of namespace keys
	recorder            record.EventRecorder
	masterResourcesPath string
	urlPattern          string

	// store namespaces with the role=kubermatic-cluster label
	nsController *framework.Controller
	nsStore      cache.Store

	// store pods with the realm=kubermatic-cluster label
	podController *framework.Controller
	podStore      cache.StoreToPodLister

	// store rcs with the realm=kubermatic-cluster label
	rcController *framework.Controller
	rcStore      cache.Indexer

	// store rcs with the realm=kubermatic-cluster label
	secretController *framework.Controller
	secretStore      cache.Indexer

	// store rcs with the realm=kubermatic-cluster label
	serviceController *framework.Controller
	serviceStore      cache.Indexer

	// non-thread safe:
	mu         sync.Mutex
	cps        map[string]provider.CloudProvider
	inProgress map[string]struct{} // in progress clusters
}

// NewController creates a cluster controller.
func NewController(
	dc string,
	client *client.Client,
	cps map[string]provider.CloudProvider,
	masterResourcesPath string,
	urlPattern string,
) (controller.Controller, error) {
	cc := &clusterController{
		dc:                  dc,
		client:              client,
		queue:               workqueue.New(),
		cps:                 cps,
		inProgress:          map[string]struct{}{},
		masterResourcesPath: masterResourcesPath,
		urlPattern:          urlPattern,
	}

	eventBroadcaster := record.NewBroadcaster()
	cc.recorder = eventBroadcaster.NewRecorder(kapi.EventSource{Component: "clustermanager"})
	eventBroadcaster.StartLogging(glog.Infof)
	eventBroadcaster.StartRecordingToSink(cc.client.Events(""))

	cc.nsStore, cc.nsController = framework.NewInformer(
		&cache.ListWatch{
			ListFunc: func() (runtime.Object, error) {
				return cc.client.Namespaces().List(
					labels.SelectorFromSet(labels.Set(map[string]string{
						kprovider.RoleLabelKey: kprovider.ClusterRoleLabel,
					})),
					fields.Everything(),
				)
			},
			WatchFunc: func(rv string) (watch.Interface, error) {
				return cc.client.Namespaces().Watch(
					labels.SelectorFromSet(labels.Set(map[string]string{
						kprovider.RoleLabelKey: kprovider.ClusterRoleLabel,
					})),
					fields.Everything(),
					rv,
				)
			},
		},
		&kapi.Namespace{},
		fullResyncPeriod,
		framework.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				ns := obj.(*kapi.Namespace)
				glog.V(4).Infof("Adding cluster %q", ns.Name)
				cc.enqueue(ns)
			},
			UpdateFunc: func(old, cur interface{}) {
				ns := cur.(*kapi.Namespace)
				glog.V(4).Infof("Updating cluster %q", ns.Name)
				cc.enqueue(ns)
			},
			DeleteFunc: func(obj interface{}) {
				ns := obj.(*kapi.Namespace)
				glog.V(4).Infof("Deleting cluster %q", ns.Name)
				cc.enqueue(ns)
			},
		},
	)

	namespaceIndexer := cache.Indexers{
		"namespace": cache.IndexFunc(cache.MetaNamespaceIndexFunc),
	}

	cc.podStore.Store, cc.podController = framework.NewInformer(
		&cache.ListWatch{
			ListFunc: func() (runtime.Object, error) {
				return cc.client.Pods(kapi.NamespaceAll).List(labels.Everything(), fields.Everything())
			},
			WatchFunc: func(rv string) (watch.Interface, error) {
				return cc.client.Pods(kapi.NamespaceAll).Watch(labels.Everything(), fields.Everything(), rv)
			},
		},
		&kapi.Pod{},
		fullResyncPeriod,
		framework.ResourceEventHandlerFuncs{},
	)

	cc.rcStore, cc.rcController = framework.NewIndexerInformer(
		&cache.ListWatch{
			ListFunc: func() (runtime.Object, error) {
				return cc.client.ReplicationControllers(kapi.NamespaceAll).List(labels.Everything())
			},
			WatchFunc: func(rv string) (watch.Interface, error) {
				return cc.client.ReplicationControllers(kapi.NamespaceAll).Watch(labels.Everything(), fields.Everything(), rv)
			},
		},
		&kapi.ReplicationController{},
		fullResyncPeriod,
		framework.ResourceEventHandlerFuncs{},
		namespaceIndexer,
	)

	cc.secretStore, cc.secretController = framework.NewIndexerInformer(
		&cache.ListWatch{
			ListFunc: func() (runtime.Object, error) {
				return cc.client.Secrets(kapi.NamespaceAll).List(labels.Everything(), fields.Everything())
			},
			WatchFunc: func(rv string) (watch.Interface, error) {
				return cc.client.Secrets(kapi.NamespaceAll).Watch(labels.Everything(), fields.Everything(), rv)
			},
		},
		&kapi.Secret{},
		fullResyncPeriod,
		framework.ResourceEventHandlerFuncs{},
		namespaceIndexer,
	)

	cc.serviceStore, cc.serviceController = framework.NewIndexerInformer(
		&cache.ListWatch{
			ListFunc: func() (runtime.Object, error) {
				return cc.client.Services(kapi.NamespaceAll).List(labels.Everything())
			},
			WatchFunc: func(rv string) (watch.Interface, error) {
				return cc.client.Services(kapi.NamespaceAll).Watch(labels.Everything(), fields.Everything(), rv)
			},
		},
		&kapi.Service{},
		fullResyncPeriod,
		framework.ResourceEventHandlerFuncs{},
		namespaceIndexer,
	)

	return cc, nil
}

func (cc *clusterController) recordClusterPhaseChange(ns *kapi.Namespace, newPhase api.ClusterPhase) {
	ref := &kapi.ObjectReference{
		Kind:      "Namespace",
		Name:      ns.Name,
		UID:       types.UID(ns.Name),
		Namespace: ns.Name,
	}
	glog.V(2).Infof("Recording phase change %s event message for namespace %s", string(newPhase), ns.Name)
	cc.recorder.Eventf(ref, string(newPhase), "Cluster phase is now: %s", newPhase)
}

func (cc *clusterController) recordClusterEvent(c *api.Cluster, reason, msg string, args ...interface{}) {
	nsName := kprovider.NamespaceName(c.Metadata.User, c.Metadata.Name)
	ref := &kapi.ObjectReference{
		Kind:      "Namespace",
		Name:      nsName,
		UID:       types.UID(nsName),
		Namespace: nsName,
	}
	glog.V(4).Infof("Recording event for namespace %q: %s", nsName, fmt.Sprintf(msg, args...))
	cc.recorder.Eventf(ref, reason, msg, args)
}

func (cc *clusterController) updateCluster(c *api.Cluster) error {
	ns := kprovider.NamespaceName(c.Metadata.User, c.Metadata.Name)
	for i := 0; i < maxUpdateRetries; i++ {
		// try to get current namespace
		oldNS, err := cc.client.Namespaces().Get(ns)
		if err != nil {
			return err
		}

		// update with latest cluster state
		newNS, err := func() (*kapi.Namespace, error) {
			cc.mu.Lock()
			defer cc.mu.Unlock()
			return kprovider.MarshalCluster(cc.cps, c, oldNS)
		}()
		if err != nil {
			return err
		}

		// try to write back namespace
		_, err = cc.client.Namespaces().Update(newNS)
		if err != nil {
			if !kerrors.IsConflict(err) {
				glog.V(4).Infof("Write conflict of namespace %q (retry=%i/%i)", ns, i, maxUpdateRetries)
				continue
			}
			return err
		}

		// record phase change events
		if c.Status.Phase != c.Status.Phase {
			cc.recordClusterPhaseChange(newNS, c.Status.Phase)
		}
		return nil
	}

	return fmt.Errorf("Updading namespace %q failed after %v retries", ns, maxUpdateRetries)
}

func (cc *clusterController) syncClusterNamespace(key string) error {
	// only run one syncCluster for each cluster in parallel
	cc.mu.Lock()
	if _, found := cc.inProgress[key]; found {
		cc.mu.Unlock()
		glog.V(4).Infof("Skipped in-progress namespace %q", key)
		return nil
	}
	cc.inProgress[key] = struct{}{}
	defer func() {
		cc.mu.Lock()
		delete(cc.inProgress, key)
		cc.mu.Unlock()
	}()
	cc.mu.Unlock()

	// get namespace
	startTime := time.Now()
	glog.V(4).Infof("Syncing cluster %q", key)
	defer func() {
		glog.V(4).Infof("Finished syncing namespace %q (%v)", key, time.Now().Sub(startTime))
	}()
	obj, exists, err := cc.nsStore.GetByKey(key)
	if err != nil {
		glog.Infof("Unable to retrieve namespace %q from store: %v", key, err)
		cc.queue.Add(key)
		return err
	}
	if !exists {
		glog.V(3).Infof("Namespace %q has been deleted", key)
		return nil
	}
	ns := obj.(*kapi.Namespace)
	if !cc.controllersHaveSynced() {
		// Sleep so we give the pod reflector goroutine a chance to run.
		time.Sleep(namespaceStoreSyncedPollPeriod)
		glog.Infof("Waiting for controllers to sync, requeuing namespace %q", ns.Name)
		cc.enqueue(ns)
		return nil
	}

	// sync cluster
	c, err := func() (*api.Cluster, error) {
		cc.mu.Lock()
		defer cc.mu.Unlock()
		return kprovider.UnmarshalCluster(cc.cps, ns)
	}()
	if err != nil {
		return err
	}

	var changedC *api.Cluster
	switch c.Status.Phase {
	case api.PendingClusterStatusPhase:
		changedC, err = cc.syncPendingCluster(c)
	case api.RunningClusterStatusPhase:
		changedC, err = cc.syncRunningCluster(c)
	default:
		glog.V(5).Infof("Ignoring cluster %q in phase %q", c.Metadata.Name, c.Status.Phase)
	}
	if err != nil {
		return err
	}

	// sync back to namespace if c was changed
	if changedC != nil {
		err = cc.updateCluster(changedC)
		if err == nil {
			return err
		}
	}

	return nil
}

func (cc *clusterController) enqueue(ns *kapi.Namespace) {
	key, err := kcontroller.KeyFunc(ns)
	if err != nil {
		glog.Errorf("Couldn't get key for object %+v: %v", ns, err)
		return
	}

	cc.queue.Add(key)
}

func (cc *clusterController) worker() {
	for {
		func() {
			nsKey, quit := cc.queue.Get()
			if quit {
				return
			}
			defer cc.queue.Done(nsKey)
			err := cc.syncClusterNamespace(nsKey.(string))
			if err != nil {
				glog.Errorf("Error syncing cluster with key %s: %v", nsKey.(string), err)
			}
		}()
	}
}

func (cc *clusterController) syncInPhase(phase api.ClusterPhase) {
	for _, obj := range cc.nsStore.List() {
		ns := obj.(*kapi.Namespace)
		if v, found := ns.Labels[kprovider.RoleLabelKey]; !found || v != kprovider.ClusterRoleLabel {
			continue
		}
		if kprovider.ClusterPhase(ns) == phase {
			cc.enqueue(ns)
		}
	}
}

func (cc *clusterController) Run(stopCh <-chan struct{}) {
	defer util.HandleCrash()
	glog.Info("Starting cluster controller")

	go cc.nsController.Run(util.NeverStop)
	go cc.podController.Run(util.NeverStop)
	go cc.rcController.Run(util.NeverStop)
	go cc.secretController.Run(util.NeverStop)
	go cc.serviceController.Run(util.NeverStop)

	for i := 0; i < workerNum; i++ {
		go util.Until(cc.worker, workerPeriod, stopCh)
	}

	go util.Until(func() { cc.syncInPhase(api.PendingClusterStatusPhase) }, pendingSyncPeriod, stopCh)
	go util.Until(func() { cc.syncInPhase(api.RunningClusterStatusPhase) }, runningSyncPeriod, stopCh)

	<-stopCh

	glog.Info("Shutting down cluster controller")
	cc.queue.ShutDown()
}

func (cc *clusterController) controllersHaveSynced() bool {
	return cc.nsController.HasSynced() &&
		cc.podController.HasSynced() &&
		cc.secretController.HasSynced() &&
		cc.rcController.HasSynced() &&
		cc.serviceController.HasSynced()
}
