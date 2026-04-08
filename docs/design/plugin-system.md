# プラグインシステム設計

> **ソースコード参照**: フックインターフェース定義は `plugin/hooks*.go`、
> レジストリは `plugin/registry.go`、公式プラグイン実装は `plugins/` を参照のこと。

## 1. 概要

### 目的

契約決済システムの以下の機能をプラガブルに拡張可能にする:

- クーポン・割引、税計算、通知、メトリクス収集、請求書生成

### 設計原則

1. **疎結合** — コアロジックとプラグインは明確に分離
2. **型安全** — インターフェースによる契約
3. **ISP準拠** — 必要なフックインターフェースのみ実装。空メソッドの強制実装は不要
4. **実行順序保証** — コアが会計基準に則った計算順序を構造的に保証

## 2. フック一覧（全20種）

### 請求計算フック

| フック | 用途 | ソース |
|--------|------|--------|
| `DiscountHook` | 割引計算 | `plugin/hooks.go` |
| `TaxHook` | 税計算（割引後の金額に対して） | `plugin/hooks.go` |
| `InvoiceLifecycleHook` | 計算前後処理（BeforeCalculation/AfterCalculation） | `plugin/hooks.go` |

### 契約ライフサイクルフック

| フック | 用途 | ソース |
|--------|------|--------|
| `OnContractCreateHook` | 契約作成時 | `plugin/hooks_contract.go` |
| `OnContractActivateHook` | 契約有効化時 | `plugin/hooks_contract.go` |
| `OnContractSuspendHook` | 契約一時停止時 | `plugin/hooks_contract.go` |
| `OnContractResumeHook` | 契約再開時 | `plugin/hooks_contract.go` |
| `OnContractCancelHook` | 契約解約時 | `plugin/hooks_contract.go` |
| `OnContractRenewHook` | 契約更新時 | `plugin/hooks_contract.go` |
| `OnContractTrialEndHook` | トライアル終了時 | `plugin/hooks_contract.go` |

### 支払いフック

| フック | 用途 | ソース |
|--------|------|--------|
| `BeforeChargeHook` | 課金前処理 | `plugin/hooks_payment.go` |
| `AfterChargeHook` | 課金後処理 | `plugin/hooks_payment.go` |
| `OnPaymentFailedHook` | 支払い失敗時 | `plugin/hooks_payment.go` |
| `OnRefundHook` | 返金時 | `plugin/hooks_payment.go` |

### メトリクスフック

| フック | 用途 | ソース |
|--------|------|--------|
| `OnContractChangeHook` | 契約変更メトリクス | `plugin/hooks_metrics.go` |
| `OnInvoiceIssuedHook` | 請求書発行メトリクス | `plugin/hooks_metrics.go` |
| `OnPaymentProcessedHook` | 支払い処理メトリクス | `plugin/hooks_metrics.go` |

### クレジットノートフック

| フック | 用途 | ソース |
|--------|------|--------|
| `OnCreditNoteIssuedHook` | CN発行時 | `plugin/hooks_creditnote.go` |
| `OnInvoiceRevisedHook` | 請求書差替時 | `plugin/hooks_creditnote.go` |

### 請求書生成フック

| フック | 用途 | ソース |
|--------|------|--------|
| `InvoiceGenerationHook` | PDF生成・送付（BuildDocument/AfterRender/AfterDelivery） | `plugin/hooks_invoicegen.go` |

## 3. コンテキスト

### CalculationContext（請求計算用）

ソース: `plugin/context.go`

型安全な計算コンテキスト。コアが各計算ステップで値を設定し、プラグインが参照する。

- `Contract()`: 契約集約
- `Subtotal()`: 基本料金（DiscountHookが参照）
- `SubtotalAfterDiscount()`: 割引後小計（TaxHookが参照）
- `AppliedDiscounts()`: 適用された割引の記録（プラグイン名・コード・金額）
- `Invoice()`: 請求書（AfterCalculation用）

### PaymentContext（支払い用）

ソース: `plugin/context_payment.go`

支払いフック専用。payment, invoice, contract を保持。

### Context（汎用）

ソース: `plugin/context.go`

契約ライフサイクル、メトリクス等の汎用コンテキスト。`metadata` map で拡張データを受け渡し。

## 4. 実行順序

### コアが保証する計算順序

請求書計算の順序はコアが構造的に保証する。
Priority値に依存しないため、プラグイン登録順のミスで会計基準違反が発生しない。

> このフロー順序は `architecture.md` セクション6.2 および `billing_service.go` の `GenerateInvoice()` と同一。

```
1. InvoiceLifecycleHook.BeforeCalculation()  ← 計算前処理
2. 料金計算（コア、契約タイプに応じて分岐）
3. DiscountHook.CalculateDiscount()          ← 割引計算（全DiscountHook）
   → 割引上限ガード（割引合計 > subtotalの場合にcap）
4. 小計算出（コア: subtotal - totalDiscount）
5. TaxHook.CalculateTax()                    ← 税計算（割引後に対して）
6. 合計算出（コア: afterDiscount + totalTax）
7. クレジット台帳からの充当（コア）          ← 残高があれば税込合計から差引
8. 請求書をdraft状態で生成 → GracePeriod後にfinalize
9. InvoiceLifecycleHook.AfterCalculation()   ← 計算後処理
```

### Priority の役割

`Priority` は**同一フック種別内**での実行順序のみに影響する。
フック種別間の順序（DiscountHook → TaxHook）はコアが制御するため、
TaxPluginのPriorityをどう設定してもDiscountHookより先に実行されることはない。

```
PriorityHighest = 0
PriorityHigh    = 100
PriorityNormal  = 500
PriorityLow     = 900
PriorityLowest  = 1000
```

例: ボリューム割引（PriorityHigh=100、先に適用）→ クーポン割引（PriorityNormal=500、後に適用）

## 5. プラグインレジストリ

ソース: `plugin/registry.go`

- `Register(plugin)`: プラグインを登録。型アサーションで実装するフックを自動分類
- `InitializeAll(ctx, configs)`: 全プラグインを初期化
- `ShutdownAll(ctx)`: 全プラグインをシャットダウン
- `Get*Hooks()`: フック種別ごとのプラグインリスト取得（Priority順）
- スレッドセーフ（`sync.RWMutex`）

## 6. フック分離の設計根拠

`InvoiceCalculationHook`（統合IF）ではなく、DiscountHook / TaxHook / InvoiceLifecycleHook に分離した理由:

1. **ISP** — 割引のみのプラグインに空のCalculateTax実装を強制しない
2. **計算順序の構造的保証** — コアが呼び出し順を制御するため、Priority値によるミスが発生しない
3. **型安全** — `CalculationContext` でプラグイン間のデータ受け渡しを型安全に行う

初版（v1.0.0）から分割設計を採用しているため、移行ガイドは不要。

## 7. 公式プラグイン

| プラグイン | フック | ソース |
|-----------|--------|--------|
| Coupon | `DiscountHook` | `plugins/coupon/` |
| Tax | `TaxHook` | `plugins/tax/` |
| InvoiceCleanup | `OnContractCancelHook` | `plugins/invoicecleanup/` |

### カスタムプラグイン作成

1. **インターフェース選択**: `plugin.Plugin` 基本IF + 必要なフックIFを実装
2. **優先度設定**: 他のプラグインとの実行順序を考慮し `Priority()` を設定
3. **設定読み込み**: `Initialize(ctx, config)` で設定を読み込む
4. **リソース解放**: `Shutdown(ctx)` でリソースを解放
5. **登録**: `plugin.Registry.Register()` で登録。型アサーションで自動的にフックに分類

```go
// 例: 割引プラグインはDiscountHookのみ実装
var _ plugin.DiscountHook = (*MyDiscountPlugin)(nil)

// 例: 複数フックを1プラグインで実装
var _ plugin.DiscountHook = (*MyBillingPlugin)(nil)
var _ plugin.TaxHook = (*MyBillingPlugin)(nil)
```

## 8. API互換性

Semantic Versioning 2.0.0 に従う。

| 変更 | バージョン |
|------|-----------|
| フックインターフェースの破壊的変更 | Major |
| 新規フックの追加 | Minor |
| バグ修正 | Patch |

破壊的変更の定義:
1. インターフェースのメソッド追加（デフォルト実装なし）
2. インターフェースのメソッドシグネチャ変更
3. インターフェースの削除・統合・分離
4. CalculationContext/Context型のフィールド削除・型変更
5. Registry APIの変更（Register/Get系メソッド）
