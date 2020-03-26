package reconciliation

import (
	"context"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	runtimeClient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/riptano/dse-operator/operator/pkg/httphelper"

	api "github.com/riptano/dse-operator/operator/pkg/apis/cassandra/v1beta1"
)

// ReconciliationContext contains all of the input necessary to calculate a list of ReconciliationActions
type ReconciliationContext struct {
	Request        *reconcile.Request
	Client         runtimeClient.Client
	Scheme         *runtime.Scheme
	Datacenter     *api.CassandraDatacenter
	NodeMgmtClient httphelper.NodeMgmtClient
	Recorder       record.EventRecorder
	ReqLogger      logr.Logger
	// According to golang recommendations the context should not be stored in a struct but given that
	// this is passed around as a parameter we feel that its a fair compromise. For further discussion
	// see: golang/go#22602
	Ctx context.Context

	Services               []*corev1.Service
	desiredRackInformation []*RackInformation
	statefulSets           []*appsv1.StatefulSet
}

// CreateReconciliationContext gathers all information needed for computeReconciliationActions into a struct.
func CreateReconciliationContext(
	req *reconcile.Request,
	cli runtimeClient.Client,
	scheme *runtime.Scheme,
	rec record.EventRecorder,
	reqLogger logr.Logger) (*ReconciliationContext, error) {

	rc := &ReconciliationContext{}
	rc.Request = req
	rc.Client = cli
	rc.Scheme = scheme
	rc.Recorder = rec
	rc.ReqLogger = reqLogger
	rc.Ctx = context.Background()

	rc.ReqLogger = rc.ReqLogger.
		WithValues("namespace", req.Namespace)

	rc.ReqLogger.Info("handler::CreateReconciliationContext")

	// Fetch the datacenter resource
	dc := &api.CassandraDatacenter{}
	if err := retrieveDatacenter(rc, req, dc); err != nil {
		rc.ReqLogger.Error(err, "error in retrieveDatacenter")
		return nil, err
	}
	rc.Datacenter = dc

	// workaround for kubernetes having problems with zero-value and nil Times
	if rc.Datacenter.Status.SuperUserUpserted.IsZero() {
		rc.Datacenter.Status.SuperUserUpserted = metav1.Unix(1, 0)
	}
	if rc.Datacenter.Status.LastServerNodeStarted.IsZero() {
		rc.Datacenter.Status.LastServerNodeStarted = metav1.Unix(1, 0)
	}

	if rc.Datacenter.Status.LastRollingRestart.IsZero() {
		dcPatch := runtimeClient.MergeFrom(dc.DeepCopy())
		dc.Status.LastRollingRestart = metav1.Now()
		err := rc.Client.Status().Patch(rc.Ctx, dc, dcPatch)
		if err != nil {
			rc.ReqLogger.Error(err, "error patching datacenter status for rolling restart")
			return nil, err
		}
	}

	httpClient, err := httphelper.BuildManagementApiHttpClient(dc, cli, rc.Ctx)
	if err != nil {
		rc.ReqLogger.Error(err, "error in BuildManagementApiHttpClient")
		return nil, err
	}

	rc.ReqLogger = rc.ReqLogger.
		WithValues("datacenterName", dc.Name).
		WithValues("clusterName", dc.Spec.ClusterName)

	protocol, err := httphelper.GetManagementApiProtocol(dc)
	if err != nil {
		rc.ReqLogger.Error(err, "error in GetManagementApiProtocol")
		return nil, err
	}

	rc.NodeMgmtClient = httphelper.NodeMgmtClient{
		Client:   httpClient,
		Log:      rc.ReqLogger,
		Protocol: protocol,
	}

	return rc, nil
}

func retrieveDatacenter(rc *ReconciliationContext, request *reconcile.Request, dc *api.CassandraDatacenter) error {
	err := rc.Client.Get(
		rc.Ctx,
		request.NamespacedName,
		dc)
	return err
}