# Hosting Integration Demo

The most practical example: shows how billing events automatically drive an external service (server provisioning) through the plugin hook system. This answers the question: **"how does a payment actually trigger my service?"**

## Scenario

A rental server (VPS) business:

| Phase | Billing Event | Server Action |
|-------|--------------|---------------|
| Sign-up | Contract created + activated | Awaiting payment |
| First payment | Payment completed | Provision server |
| Renewal fails | Payment declined | Contract suspended -> Stop server |
| Card updated | Payment retry succeeds | Contract resumed -> Restart server |
| Cancellation | Contract cancelled | Terminate server |

## What You'll See

```
Phase 2: First Payment & Server Provisioning
  >> [ServerManager] SERVER PROVISIONED: allocating CPU/RAM/disk, installing OS
  🟢 Server [...]: running

Phase 3: Payment Failed -> Server Suspended
  Payment failed: card declined: insufficient funds
  >> [ServerManager] SERVER STOPPED: VM suspended, data preserved
  🔴 Server [...]: stopped

Phase 4: Payment Retry -> Server Resumed
  >> [ServerManager] SERVER RESTARTED: VM resumed, services starting
  🟢 Server [...]: running

Phase 5: Cancellation -> Server Terminated
  >> [ServerManager] SERVER TERMINATED: VM deleted, backup created, IP released
  ⚫ Server [...]: terminated
```

## How It Works

The `ServerProvisioningPlugin` implements 6 hook interfaces in a single plugin:

```go
// Contract lifecycle
func (p *...) OnContractCreate(...)   // Prepare resources
func (p *...) OnContractActivate(...) // Mark ready
func (p *...) OnContractSuspend(...)  // STOP server
func (p *...) OnContractResume(...)   // RESTART server
func (p *...) OnContractCancel(...)   // TERMINATE server

// Payment
func (p *...) AfterCharge(...)        // PROVISION server
```

Register it like any other plugin -- the billing core remains decoupled:

```go
registry.Register(newServerProvisioningPlugin(serverManager))
```

## Key Concepts

| Concept | Description |
|---------|-------------|
| **Multi-hook plugin** | One plugin implementing multiple hook interfaces |
| **Decoupled integration** | Billing core knows nothing about servers |
| **Composable** | Add notification, monitoring, or DNS plugins alongside provisioning |
| **Event-driven** | No polling; actions are triggered by lifecycle events |

## Real-World Applications

This pattern works for any service activated by billing:

- **Hosting**: Server provisioning, DNS setup, SSL certificates
- **SaaS**: Seat allocation, feature flag toggling
- **Domain registration**: Register/renew/transfer domains
- **Cloud**: Kubernetes namespace creation, resource quota
- **CDN**: Edge configuration, cache purge

## Run

```bash
go run ./examples/hosting-integration-demo/
```
