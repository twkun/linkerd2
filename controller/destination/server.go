package destination

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	pb "github.com/linkerd/linkerd2-proxy-api/go/destination"
	"github.com/linkerd/linkerd2/controller/k8s"
	"github.com/linkerd/linkerd2/pkg/prometheus"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"
)

const podIpIndexName = "ip"

type server struct {
	k8sAPI    *k8s.API
	resolvers []streamingDestinationResolver
	enableTLS bool
}

// The Destination service serves service discovery information to the proxy.
// This implementation supports the "k8s" destination scheme and expects
// destination paths to be of the form:
// <service>.<namespace>.svc.cluster.local:<port>
//
// If the port is omitted, 80 is used as a default.  If the namespace is
// omitted, "default" is used as a default.append
//
// Addresses for the given destination are fetched from the Kubernetes Endpoints
// API.
func NewServer(addr, k8sDNSZone string, enableTLS bool, k8sAPI *k8s.API, done chan struct{}) (*grpc.Server, net.Listener, error) {
	k8sAPI.Pod().Informer().AddIndexers(cache.Indexers{podIpIndexName: indexPodByIp})
	resolvers, err := buildResolversList(k8sDNSZone, k8sAPI)
	if err != nil {
		return nil, nil, err
	}

	srv := server{
		k8sAPI:    k8sAPI,
		resolvers: resolvers,
		enableTLS: enableTLS,
	}

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, err
	}

	s := prometheus.NewGrpcServer()
	pb.RegisterDestinationServer(s, &srv)

	go func() {
		<-done
		for _, resolver := range resolvers {
			resolver.stop()
		}
	}()

	return s, lis, nil
}

func (s *server) Get(dest *pb.GetDestination, stream pb.Destination_GetServer) error {
	log.Debugf("Get %v", dest)
	if dest.Scheme != "k8s" {
		err := fmt.Errorf("Unsupported scheme %v", dest.Scheme)
		log.Error(err)
		return err
	}
	hostPort := strings.Split(dest.Path, ":")
	if len(hostPort) > 2 {
		err := fmt.Errorf("Invalid destination %s", dest.Path)
		log.Error(err)
		return err
	}
	host := hostPort[0]
	port := 80
	if len(hostPort) == 2 {
		var err error
		port, err = strconv.Atoi(hostPort[1])
		if err != nil {
			err = fmt.Errorf("Invalid port %s", hostPort[1])
			log.Error(err)
			return err
		}
	}

	return s.streamResolutionUsingCorrectResolverFor(host, port, stream)
}

func indexPodByIp(obj interface{}) ([]string, error) {
	if pod, ok := obj.(*v1.Pod); ok {
		return []string{pod.Status.PodIP}, nil
	}
	return []string{""}, fmt.Errorf("object is not a pod")
}

func (s *server) podsByIp(ip string) ([]*v1.Pod, error) {
	objs, err := s.k8sAPI.Pod().Informer().GetIndexer().ByIndex(podIpIndexName, ip)
	if err != nil {
		return nil, err
	}
	pods := make([]*v1.Pod, 0)
	for _, obj := range objs {
		pod, ok := obj.(*v1.Pod)
		if !ok {
			return nil, fmt.Errorf("not a pod")
		}
		pods = append(pods, pod)
	}
	return pods, nil
}

func (s *server) streamResolutionUsingCorrectResolverFor(host string, port int, stream pb.Destination_GetServer) error {
	listener := newEndpointListener(stream, s.podsByIp, s.k8sAPI.GetOwnerKindAndName, s.enableTLS)

	for _, resolver := range s.resolvers {
		resolverCanResolve, err := resolver.canResolve(host, port)
		if err != nil {
			return fmt.Errorf("resolver [%+v] found error resolving host [%s] port[%d]: %v", resolver, host, port, err)
		}
		if resolverCanResolve {
			return resolver.streamResolution(host, port, listener)
		}
	}
	return fmt.Errorf("cannot find resolver for host [%s] port [%d]", host, port)
}

func buildResolversList(k8sDNSZone string, k8sAPI *k8s.API) ([]streamingDestinationResolver, error) {
	var k8sDNSZoneLabels []string
	if k8sDNSZone == "" {
		k8sDNSZoneLabels = []string{}
	} else {
		var err error
		k8sDNSZoneLabels, err = splitDNSName(k8sDNSZone)
		if err != nil {
			return nil, err
		}
	}

	k8sResolver := newK8sResolver(k8sDNSZoneLabels, k8sAPI)

	log.Infof("Adding k8s name resolver")

	return []streamingDestinationResolver{k8sResolver}, nil
}
