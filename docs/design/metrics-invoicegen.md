# メトリクス・請求書生成設計

> **ソースコード参照**: メトリクスフックは `plugin/hooks_metrics.go`、
> 請求書生成フックは `plugin/hooks_invoicegen.go` を参照。

## 1. 概要

2つのプラグインアダプターカテゴリの設計:

| Adapter | 責務 | フック |
|---------|------|--------|
| Metrics & Analytics | 契約・請求・決済のKPI収集・分析 | `OnContractChangeHook`, `OnInvoiceIssuedHook`, `OnPaymentProcessedHook` |
| Invoice Generation | 請求書のPDF/HTML生成・送付 | `InvoiceGenerationHook` |

## 2. メトリクス Adapter

### メトリクス定義

| メトリクス | 説明 |
|-----------|------|
| MRR | 月次経常収益 |
| Churn Rate | 解約率 |
| ARPU | ユーザーあたり収益 |
| Revenue | 期間内収益 |
| Outstanding | 未収金額 |

### OnContractChangeHook

契約の各種変更イベントを受け取り、メトリクスを更新する。

`ContractChangeEvent`:
- `ChangeType`: created, activated, suspended, resumed, cancelled, renewed, trial_end
- `OldStatus` / `NewStatus`: ステータス変更の前後
- `OldPriceID` / `NewPriceID`: 価格変更時
- `MRRChange`: MRR変動額

### OnInvoiceIssuedHook / OnPaymentProcessedHook

請求書発行時・支払い処理時にメトリクスを更新。

### レポートクエリ

サービス側が実装する `MetricsQueryService`:
- `GetMRR(ctx, asOf)` — 指定時点のMRR
- `GetChurnRate(ctx, period)` — 期間内解約率
- `GetRevenueReport(ctx, period)` — 収益レポート

## 3. 請求書生成 Adapter

### InvoiceGenerationHook

3フェーズの請求書生成パイプライン:

| フェーズ | メソッド | 説明 |
|---------|---------|------|
| 構築 | `BuildDocument(ctx, invoice, doc)` | 請求書ドキュメントの構築・カスタマイズ |
| レンダリング後 | `AfterRender(ctx, doc, rendered)` | レンダリング結果の後処理（署名、アーカイブ等） |
| 送付後 | `AfterDelivery(ctx, doc, result)` | 送付結果の処理（ログ、通知等） |

### InvoiceDocument

レンダリング対象のデータモデル:
- 発行者情報（IssuerInfo）、請求先情報、明細行（LineItems）
- 小計、割引、税額、合計、適用クレジット、請求額
- 請求期間、発行日、支払期限

### サービス側の実装

- `InvoiceRenderer`: PDF/HTML レンダリング（wkhtmltopdf, chromedp, gotenberg等）
- `InvoiceDelivery`: メール送付（SendGrid, SES等）
- `InvoiceStorage`: PDF保存（S3, GCS等）

## 4. ディレクトリ構成

```
plugin/
├── hooks_metrics.go      # OnContractChangeHook, OnInvoiceIssuedHook, OnPaymentProcessedHook
├── hooks_invoicegen.go   # InvoiceGenerationHook, InvoiceDocument, DeliveryResult
└── context.go            # ContractChangeEvent
```

サービス側:
```
infrastructure/
├── metrics/              # MetricsCollector 実装（Prometheus, Datadog等）
└── invoicegen/           # InvoiceRenderer, InvoiceDelivery, InvoiceStorage 実装
```
