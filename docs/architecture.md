# Contract Billing Core - アーキテクチャ概要

**イベントソーシング + プラグインアーキテクチャによる契約決済OSSパッケージ**

## 1. 概要

### 1.1 目的

SaaSやサービス事業において、契約・決済ロジックは本質的に類似している。本パッケージは以下を提供する：

- 契約ライフサイクル管理（買い切り / サブスクリプション / 従量課金）
- イベントソーシングによる全操作の完全な監査証跡
- プラグインアーキテクチャによる拡張性（クーポン、割引、税計算など）

### 1.2 システム構成

```mermaid
graph TB
    subgraph Application
        subgraph Plugins
            CP[Coupon Plugin]
            TP[Tax Plugin]
            CUP[Custom Plugin<br/>User Defined]
        end
        
        CP --> PAI
        TP --> PAI
        CUP --> PAI
        PAI[Plugin Adapter Interface]
        
        subgraph ContractCore[Contract Core]
            SE[Subscription Engine]
            OE[One-Time Engine]
            UE[Usage-Based Engine]
        end
        
        PAI --> ContractCore
        
        ES[(Event Store<br/>Append-Only Event Log)]
        ContractCore --> ES
    end
```

## 2. 設計原則

### 2.1 依存逆転の原則（DIP）

```mermaid
graph TB
    subgraph PL[Presentation Layer]
        PL_DESC[HTTP Handler, gRPC, CLI]
    end
    
    subgraph AL[Application Layer]
        AL_DESC[UseCase, Command/Query Handler]
    end
    
    subgraph DL[Domain Layer]
        DL_DESC[Entity, Value Object,<br/>Domain Service, Repository Interface]
        DL_NOTE[※外部依存なし]
    end
    
    subgraph IL[Infrastructure Layer]
        IL_DESC[PostgreSQL, MySQL,<br/>DynamoDB, EventStore...]
    end
    
    PL -->|depends on| AL
    AL -->|depends on| DL
    IL -.->|implements| DL
```

### 2.2 CQRS（コマンド・クエリ分離）

| 項目 | 決定 |
|------|------|
| CQRS | **簡易CQRS** |
| Projection更新 | **利用者が選択** |
| 説明 | 同一DBでProjectionテーブルを使用。同期/非同期をオプションで指定可能 |

### 2.3 その他の設計方針

| 項目 | 決定 | 備考 |
|------|------|------|
| マルチテナント | サービス側に委ねる | OSS側では対応しない |
| タイムゾーン | **UTC固定** | 全イベント時刻はUTC |
| 請求サイクル | 選択肢をOSSで提供 | 日次/週次/月次/年次 |

## 3. パッケージ構成

```
github.com/contract-to-cash/core/
├── domain/                      # ドメイン層（依存なし）
│   ├── contract/
│   │   ├── aggregate.go         # イベントソーシング集約
│   │   ├── repository.go        # インターフェース定義
│   │   ├── events.go
│   │   └── entity.go
│   ├── invoice/
│   ├── payment/
│   ├── balance/                 # クレジット台帳
│   ├── billing/                 # 計算抽象化
│   ├── pricing/                 # 不変Price、料金モデル
│   ├── product/                 # Product定義
│   ├── usage/
│   └── shared/                  # 共通値オブジェクト
│       ├── money.go
│       ├── daterange.go
│       └── identifier.go
│
├── application/                 # アプリケーション層
│   ├── port/                    # 外部連携IF（PaymentGateway等）
│   ├── query/
│   ├── projection/
│   ├── tx/                      # トランザクション管理（TxManager, Saga）
│   └── service/                 # BillingService, PaymentService, SnapshotService, CreditNoteService
│
├── plugin/                      # プラグインシステム
│   ├── registry.go
│   ├── hooks.go
│   └── context.go
│
├── eventstore/                  # Event Store インターフェース
│   ├── store.go
│   ├── event.go
│   └── snapshot.go
│
├── batch/                       # バッチ処理
│   ├── processor.go
│   └── ...
│
├── infrastructure/              # インフラ実装
│   └── inmemory/               # テスト・デモ用インメモリ実装
│
└── plugins/                     # 公式プラグイン
    ├── coupon/
    ├── tax/
    └── invoicecleanup/
```

## 4. 主要コンポーネント

### 4.1 コアドメイン

| ドメイン | 責務 |
|---------|------|
| **Contract** | 契約のライフサイクル管理（作成、有効化、一時停止、解約、更新） |
| **Invoice** | 請求書の生成、発行、支払い記録 |
| **CreditNote** | クレジットノートの作成・発行・適用・返金、請求書再発行（リビジョンチェーン） |
| **Payment** | 決済処理、返金 |
| **Usage** | 従量課金のメトリクス記録・集計 |

### 4.2 契約タイプ

| タイプ | 説明 |
|--------|------|
| `one_time` | 買い切り |
| `subscription` | サブスクリプション（定期課金） |
| `usage_based` | 従量課金 |

### 4.3 契約ステータス

```go
type ContractStatus string

const (
    ContractStatusDraft     ContractStatus = "draft"
    ContractStatusTrialing  ContractStatus = "trialing"   // トライアル
    ContractStatusActive    ContractStatus = "active"
    ContractStatusPastDue   ContractStatus = "past_due"   // 支払い遅延（Dunning中）
    ContractStatusSuspended ContractStatus = "suspended"
    ContractStatusCancelled ContractStatus = "cancelled"
    ContractStatusExpired   ContractStatus = "expired"
)
```

## 5. プラグインシステム

### 5.1 拡張ポイント

全フックがISP（インターフェース分離の原則）に準拠。必要なフックだけ実装すればよい。

| カテゴリ | フック例 | 用途 |
|---------|---------|------|
| **請求計算** | `DiscountHook`, `TaxHook`, `InvoiceLifecycleHook` | 割引・税計算、計算前後処理 |
| **契約ライフサイクル** | `OnContractCreateHook`, `OnContractCancelHook` 等 | 契約の各イベントに個別対応 |
| **支払い** | `BeforeChargeHook`, `AfterChargeHook` 等 | 課金前後、失敗時、返金時 |
| **クレジットノート** | `OnCreditNoteIssuedHook`, `OnInvoiceRevisedHook` | クレジットノート発行、請求書再発行 |
| **メトリクス** | `OnContractChangeHook`, `OnInvoiceIssuedHook` 等 | KPI収集 |
| **請求書生成** | `InvoiceGenerationHook` | PDF生成、送付 |

### 5.2 実行順序

コアが会計基準に則った計算順序を構造的に保証する：

```
1. InvoiceLifecycleHook.BeforeCalculation()  ← 計算前処理
2. 料金計算（コア、契約タイプに応じて分岐）
   - subscription: 固定料金
   - usage_based:  UsageRecord集計 → 含有枠差引 → PricingModel適用
   - one_time:     固定料金（1回のみ）
   - ハイブリッド:  基本料金 + 従量料金
3. DiscountHook.CalculateDiscount()          ← 割引計算
   → 割引上限ガード（割引合計 > subtotalの場合にcap）
4. 小計算出（コア: subtotal - totalDiscount）
5. TaxHook.CalculateTax()                    ← 税計算（割引後に対して）
6. 合計算出（コア: afterDiscount + totalTax）
7. クレジット台帳からの充当（コア）          ← 残高があれば税込合計から差引
8. 請求書をdraft状態で生成 → GracePeriod後にfinalize
9. InvoiceLifecycleHook.AfterCalculation()   ← 計算後処理
```

## 6. 関連ドキュメント

| ドキュメント | 内容 |
|-------------|------|
| [ドメインモデル設計](./design/domain-model.md) | エンティティ、値オブジェクトの詳細設計 |
| [イベントソーシング設計](./design/event-sourcing.md) | Event Store、時点再構築の詳細 |
| [プラグインシステム設計](./design/plugin-system.md) | プラグインの実装方法 |
| [決済ゲートウェイ設計](./design/payment-gateway.md) | 決済インターフェースの詳細 |
| [メトリクス・インボイス生成設計](./design/metrics-invoicegen.md) | 集計・レポート、請求書発行 |
| [設計決定事項](./decisions/design-decisions.md) | 設計上の判断とその理由 |
| [利用ガイド](./guides/usage-guide.md) | サービスへの導入方法 |
