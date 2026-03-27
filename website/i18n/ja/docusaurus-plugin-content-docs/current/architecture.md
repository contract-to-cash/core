---
sidebar_position: 3
---

# アーキテクチャ

Contract Billing Coreは、ドメイン駆動設計（DDD）に基づくレイヤードアーキテクチャを採用し、イベントソーシングを中核に据えています。

## パッケージ構成

```
github.com/contract-to-cash/core/
├── domain/              # ドメイン層 — エンティティ、値オブジェクト、集約
│   ├── contract/        # 契約集約（イベントソース）
│   ├── invoice/         # 請求書エンティティ
│   ├── payment/         # 支払いエンティティ
│   ├── credit/          # クレジット台帳エントリ
│   ├── usage/           # 使用量レコードとサマリー
│   ├── pricing/         # Priceエンティティと価格モデル
│   ├── product/         # Productエンティティ
│   └── shared/          # 共有値オブジェクト（Money, DateRange, ID, Clock）
├── application/         # アプリケーション層 — サービス、ポート、クエリ
│   ├── service/         # BillingService, PaymentService, SnapshotService
│   ├── port/            # PaymentGatewayインターフェース（ヘキサゴナルポート）
│   ├── query/           # TemporalQueryService
│   └── projection/      # プロジェクションサービス（読み取りモデル）
├── eventstore/          # イベントソーシング基盤
├── plugin/              # プラグインシステム
├── plugins/             # 公式プラグイン実装
├── infrastructure/      # インフラ実装
│   └── inmemory/        # インメモリ実装（テスト・デモ用）
├── batch/               # バッチプロセッサ
└── examples/            # 実行可能サンプル
```

## 依存関係の方向

```mermaid
graph LR
    App[Application] --> Domain
    Infra[Infrastructure] --> Domain
    App --> Plugin
    Plugin --> Domain
    ES[EventStore] --> Domain
```

- **Domain** は外部依存ゼロ。リポジトリインターフェースとドメインイベントを定義。
- **Application** はドメイン操作とプラグイン実行をオーケストレーション。
- **Infrastructure** はドメインインターフェース（リポジトリ、イベントストア）を実装。
- **Plugin** はフックを通じてアプリケーション動作を拡張。

## 集約ルートとしてのContract

`ContractAggregate`は中心的なイベントソースエンティティです。すべての状態変更がドメインイベントを生成します：

```
ContractCreatedEvent → ContractActivatedEvent → ContractRenewedEvent → ...
```

状態遷移は厳格な状態機械に従います：

```mermaid
stateDiagram-v2
    [*] --> draft
    draft --> trialing
    draft --> active
    trialing --> active
    trialing --> cancelled
    active --> suspended
    suspended --> active
    active --> cancelled
    active --> expired
    suspended --> cancelled
```

## 請求書生成パイプライン

`BillingService.GenerateInvoice()`は14ステップのパイプラインを実行します：

1. 契約集約のロード
2. **BeforeCalculation**フック（InvoiceLifecycleHook）
3. 小計計算（Priceエンティティのロード、PriceOverride適用）
4. **DiscountHook**の適用（複数、優先度順）
5. 割引の上限キャップ
6. 割引後小計の計算
7. **TaxHook**の適用（割引後金額に対して）
8. 合計計算
9. クレジット適用（FIFO消費）
10. 請求金額の計算
11. ドラフト請求書の作成
12. 請求書の保存
13. **AfterCalculation**フック（InvoiceLifecycleHook）
14. 請求書を返却

## 決済処理

`PaymentService.ProcessPayment()`は決済手段を階層的に解決します：

```
1. ProcessPaymentInput内の明示的PaymentMethodID
2. Invoice.PaymentMethodID
3. Contract.PaymentMethodID
4. Customer.DefaultPaymentMethodID
```

:::note
ContractおよびCustomerレベルのフォールバックには、`PaymentService`に`contractRepo`を渡す必要があります。省略した場合、解決はInvoiceレベルで止まります。
:::

## Product/Priceモデル

Stripeパターン（2020年にモノリシックPlanを廃止）に準拠：

- **Product** — 何を売るか（名前、機能、使用量メトリクス）
- **Price** — どう課金するか（金額、通貨、課金サイクル、価格モデル）。**作成後は不変。**

この分離により以下が可能：
- 既存サブスクライバーに影響を与えない価格改定（グランドファザリング）
- 同一プロダクトに複数価格（月額/年額、マルチ通貨）
- `pendingPriceID`によるクリーンな価格移行

## 設計判断

| 判断 | 理由 |
|------|------|
| Contractのみイベントソーシング | 契約は完全な監査証跡が必要。請求書/支払いはシンプルなCRUDエンティティ |
| 不変Priceエンティティ | サイレントな再価格設定を防止。価格変更は新オブジェクトを作成 |
| プラグイン優先度順序 | 割引は税の前に実行。監査フックが最初に実行 |
| FIFOクレジット消費 | クレジット台帳の業界標準。予測可能な動作 |
| 半開区間DateRange `[start, end)` | 課金期間のギャップと重複を防止 |
| Moneyに`big.Rat` | 任意精度で浮動小数点の丸め誤差を回避 |
