// Copyright 2016 ~ 2018 AlexStocks(https://github.com/AlexStocks).
// All rights reserved.  Use of this source code is
// governed by Apache License 2.0.

// Package etcdv3 provides an etcd version 3 gxregistry
// ref: https://github.com/micro/go-plugins/blob/master/registry/etcdv3/etcdv3.go
package etcdv3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"
)

import (
	"github.com/AlexStocks/goext/database/etcd"
	"github.com/AlexStocks/goext/micro/registry"
	etcdv3 "github.com/coreos/etcd/clientv3"
	jerrors "github.com/juju/errors"

	"github.com/coreos/etcd/etcdserver/api/v3rpc/rpctypes"
	hash "github.com/mitchellh/hashstructure"
)

var (
	prefix = "/micro-gxregistry"
)

type Registry struct {
	client  *gxetcd.LeaseClient
	options gxregistry.Options
	sync.Mutex
	register map[string]uint64
	leases   map[string]etcdv3.LeaseID
}

func init() {
	// cmd.DefaultRegistries["etcdv3"] = NewRegistry
}

func encode(s *gxregistry.Service) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func decode(ds []byte) *gxregistry.Service {
	var s *gxregistry.Service
	json.Unmarshal(ds, &s)
	return s
}

func nodePath(s, id string) string {
	service := strings.Replace(s, "/", "-", -1)
	node := strings.Replace(id, "/", "-", -1)
	return path.Join(prefix, service, node)
}

func servicePath(s string) string {
	return path.Join(prefix, strings.Replace(s, "/", "-", -1))
}

func (e *Registry) Options() gxregistry.Options {
	return e.options
}

func (e *Registry) Deregister(sv interface{}) error {
	// s *gxregistry.Service
	s, ok := sv.(*gxregistry.Service)
	if !ok {
		return jerrors.Errorf("@service:%+v type is not gxregistry.Service", sv)
	}

	if len(s.Nodes) == 0 {
		return errors.New("Require at least one node")
	}

	e.Lock()
	// delete our hash of the service
	delete(e.register, s.Name)
	// delete our lease of the service
	delete(e.leases, s.Name)
	e.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), e.options.Timeout)
	defer cancel()

	for _, node := range s.Nodes {
		_, err := e.client.EtcdClient().Delete(ctx, nodePath(s.Name, node.Id))
		if err != nil {
			return err
		}
	}
	return nil
}

func (e *Registry) Register(sv interface{}, opts ...gxregistry.RegisterOption) error {
	s, ok := sv.(*gxregistry.Service)
	if !ok {
		return jerrors.Errorf("@service:%+v type is not gxregistry.Service", sv)
	}
	if len(s.Nodes) == 0 {
		return errors.New("Require at least one node")
	}

	var leaseNotFound bool
	//refreshing lease if existing
	leaseID, ok := e.leases[s.Name]
	if ok {
		if _, err := e.client.EtcdClient().KeepAliveOnce(context.TODO(), leaseID); err != nil {
			if err != rpctypes.ErrLeaseNotFound {
				return err
			}

			// lease not found do register
			leaseNotFound = true
		}
	}

	// create hash of service; uint64
	h, err := hash.Hash(s, nil)
	if err != nil {
		return err
	}

	// get existing hash
	e.Lock()
	v, ok := e.register[s.Name]
	e.Unlock()

	// the service is unchanged, skip registering
	if ok && v == h && !leaseNotFound {
		return nil
	}

	service := &gxregistry.Service{
		Name:      s.Name,
		Version:   s.Version,
		Metadata:  s.Metadata,
		Endpoints: s.Endpoints,
	}

	var options gxregistry.RegisterOptions
	for _, o := range opts {
		o(&options)
	}

	ctx, cancel := context.WithTimeout(context.Background(), e.options.Timeout)
	defer cancel()

	var lgr *etcdv3.LeaseGrantResponse
	if options.TTL.Seconds() > 0 {
		lgr, err = e.client.EtcdClient().Grant(ctx, int64(options.TTL.Seconds()))
		if err != nil {
			return err
		}
	}

	for _, node := range s.Nodes {
		service.Nodes = []*gxregistry.Node{node}
		if lgr != nil {
			_, err = e.client.EtcdClient().Put(ctx, nodePath(service.Name, node.Id), encode(service), etcdv3.WithLease(lgr.ID))
		} else {
			_, err = e.client.EtcdClient().Put(ctx, nodePath(service.Name, node.Id), encode(service))
		}
		if err != nil {
			return err
		}
	}

	e.Lock()
	// save our hash of the service
	e.register[s.Name] = h
	// save our leaseID of the service
	if lgr != nil {
		e.leases[s.Name] = lgr.ID
	}
	e.Unlock()

	return nil
}

func (e *Registry) GetService(name string) ([]*gxregistry.Service, error) {
	ctx, cancel := context.WithTimeout(context.Background(), e.options.Timeout)
	defer cancel()

	rsp, err := e.client.EtcdClient().Get(ctx, servicePath(name)+"/", etcdv3.WithPrefix(), etcdv3.WithSort(etcdv3.SortByKey,
		etcdv3.SortDescend))
	if err != nil {
		return nil, err
	}

	if len(rsp.Kvs) == 0 {
		return nil, gxregistry.ErrorRegistryNotFound
	}

	serviceMap := map[string]*gxregistry.Service{}

	for _, n := range rsp.Kvs {
		if sn := decode(n.Value); sn != nil {
			s, ok := serviceMap[sn.Version]
			if !ok {
				s = &gxregistry.Service{
					Name:      sn.Name,
					Version:   sn.Version,
					Metadata:  sn.Metadata,
					Endpoints: sn.Endpoints,
				}
				serviceMap[s.Version] = s
			}

			for _, node := range sn.Nodes {
				s.Nodes = append(s.Nodes, node)
			}
		}
	}

	var services []*gxregistry.Service
	for _, service := range serviceMap {
		services = append(services, service)
	}
	return services, nil
}

func (e *Registry) ListServices() ([]*gxregistry.Service, error) {
	var services []*gxregistry.Service
	nameSet := make(map[string]struct{})

	ctx, cancel := context.WithTimeout(context.Background(), e.options.Timeout)
	defer cancel()

	rsp, err := e.client.EtcdClient().Get(ctx, prefix, etcdv3.WithPrefix(), etcdv3.WithSort(etcdv3.SortByKey, etcdv3.SortDescend))
	if err != nil {
		return nil, err
	}

	if len(rsp.Kvs) == 0 {
		return []*gxregistry.Service{}, nil
	}

	for _, n := range rsp.Kvs {
		if sn := decode(n.Value); sn != nil {
			nameSet[sn.Name] = struct{}{}
		}
	}
	for k := range nameSet {
		service := &gxregistry.Service{}
		service.Name = k
		services = append(services, service)
	}

	return services, nil
}

func (e *Registry) Watch(opts ...gxregistry.WatchOption) (gxregistry.Watcher, error) {
	return nil, nil
	// return newEtcdv3Watcher(e, e.options.Timeout, opts...)
}

func (e *Registry) String() string {
	return "Registry"
}

func NewRegistry(opts ...gxregistry.Option) gxregistry.Registry {
	config := etcdv3.Config{
		Endpoints: []string{"127.0.0.1:2379"},
	}

	var options gxregistry.Options
	for _, o := range opts {
		o(&options)
	}

	if options.Timeout == 0 {
		options.Timeout = 5 * time.Second
	}

	var cAddrs []string

	for _, addr := range options.Addrs {
		if len(addr) == 0 {
			continue
		}
		cAddrs = append(cAddrs, addr)
	}

	// if we got addrs then we'll update
	if len(cAddrs) > 0 {
		config.Endpoints = cAddrs
	}

	cli, err := etcdv3.New(config)
	if err != nil {
		panic(fmt.Errorf("etcdv3.New(config:%+v) = error:%s", config, err))
	}
	gxcli, err := gxetcd.NewLeaseClient(cli)
	if err != nil {
		panic(fmt.Errorf("gxetcd.NewLeaseClient() = error:%s", err))
	}

	return &Registry{
		client:   gxcli,
		options:  options,
		register: make(map[string]uint64),
		leases:   make(map[string]etcdv3.LeaseID),
	}
}