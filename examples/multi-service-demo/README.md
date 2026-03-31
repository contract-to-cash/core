# Multi-Service Demo

Shows how multiple independent service plugins coexist in a single billing system, each reacting only to its own contract types via PriceID-based filtering.

## Scenario

A hosting company selling 3 products:

| Product | PriceID prefix | Plugin | Actions |
|---------|--------------|--------|---------|
| VPS Server | `price-vps-*` | ServerPlugin | Provision/stop/restart/terminate VM |
| SSL Certificate | `price-ssl-*` | SSLPlugin | Issue/suspend/reactivate/revoke cert |
| Domain Name | `price-domain-*` | DomainPlugin | Register/suspend/restore/release domain |

## What You'll See

```
Phase 1: Customer purchases 3 products
  🟢 [Server] PROVISIONED VM ...
  🔒 [SSL] ISSUED certificate ...
  🌐 [Domain] REGISTERED domain ...

Phase 2: VPS payment fails -> only VPS suspended
  🔴 [Server] STOPPED VM ...
  (SSL and Domain unaffected)

Phase 3: VPS payment succeeds -> VPS resumed
  🟢 [Server] RESTARTED VM ...

Phase 4: Customer cancels domain only
  🚫 [Domain] RELEASED domain ...
  (VPS and SSL continue running)

Final:
  🟢 VPS Server        active
  🟢 SSL Certificate   active
  ⚫ Domain Name       cancelled
```

## How It Works

All three plugins are registered simultaneously. Every contract event is delivered to every plugin, but each plugin filters by PriceID prefix:

```go
func (p *ServerPlugin) handles(c *contract.ContractAggregate) bool {
    return strings.HasPrefix(string(c.PriceID()), "price-vps-")
}

func (p *ServerPlugin) OnContractSuspend(ctx *plugin.Context, c *contract.ContractAggregate) error {
    if !p.handles(c) {
        return nil  // not my responsibility
    }
    return p.stopServer(c)
}
```

## Key Concepts

| Concept | Description |
|---------|-------------|
| **PriceID-based routing** | Each plugin filters events by PriceID prefix -- simple and explicit |
| **Single responsibility** | Each plugin handles exactly one service type |
| **Zero coupling** | Plugins don't know about each other; billing core doesn't know about services |
| **Additive extensibility** | New product = new plugin. No changes to existing code |

## Run

```bash
go run ./examples/multi-service-demo/
```
