package infra

// TODO Phase 2: Port from nvoi-platform/internal/infra/k3s.go (~212 lines)
//   - SetupK3s(ctx, master, workers, privKey, w) → error
//   - configureK3sRegistry(ctx, ssh, registryHost)
//   - installK3sServer(ctx, ssh, publicIP, privateIP, privateIface)
//   - joinK3sWorker(ctx, masterClient, worker, privKey, masterPrivateIP)
//   - labelK3sNodes(ctx, masterClient, nodes)
//   - waitForK3sReady(ctx, masterClient)
//   - discoverPrivateInterface(ctx, ssh, privateIP)
//
// Also port: internal/infra/registry.go (~68 lines)
//   - EnsureRegistry(ctx, masterClient, registryAddr, w)
//   - ConfigureNodes(ctx, allNodes, registryAddr, privKey, w)
