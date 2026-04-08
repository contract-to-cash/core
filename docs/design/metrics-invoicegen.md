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

### アダプター一覧

| Adapter | 責務 | サービス側実装例 |
|---------|------|-----------------|
| `MetricsCollector` | 契約・請求・決済の集計 | 日次バッチ、リアルタイム |
| `AnalyticsExporter` | 外部分析ツールへのエクスポート | BigQuery, Redshift |
| `InvoiceRenderer` | 請求書のレンダリング | wkhtmltopdf, chromedp |
| `InvoiceDelivery` | 請求書の送付 | SendGrid, SES |
| `InvoiceStorage` | 請求書の保管 | S3, GCS（電子帳簿保存法対応） |

### メトリクス型設計

| メトリクス型 | 主要フィールド | 説明 |
|-------------|---------------|------|
| `ContractMetrics` | ActiveCount, TrialingCount, ChurnedCount, MRR | 契約状態の集計 |
| `RevenueMetrics` | Revenue, MRR, ARR, ARPU, MRRBreakdown | 収益関連。MRR内訳を含む |
| `PaymentMetrics` | TotalCollected, SuccessRate, AverageAmount | 決済処理の集計 |
| `InvoiceMetrics` | IssuedCount, PaidCount, OverdueCount, Outstanding | 請求書の状態集計 |
| `ChurnMetrics` | ChurnRate, ChurnedCount, ChurnedMRR, RetentionRate | 解約関連 |
| `CohortMetrics` | CohortMonth, InitialCount, Periods, RetentionRates | コホート分析 |

#### MRR内訳（MRRBreakdown）

| フィールド | 説明 |
|-----------|------|
| `NewMRR` | 新規契約からのMRR |
| `ExpansionMRR` | アップグレードによるMRR増加 |
| `ContractionMRR` | ダウングレードによるMRR減少 |
| `ChurnMRR` | 解約によるMRR損失 |
| `NetNewMRR` | 純増MRR（New + Expansion - Contraction - Churn） |

### 3層アダプター設計

| 層 | インターフェース | 責務 |
|----|----------------|------|
| Collector | `MetricsCollector`（6メソッド） | 各種メトリクスの収集・MRR計算 |
| Store | `MetricsStore`（10メソッド） | メトリクスの永続化・取得・範囲検索 |
| Exporter | `Exporter`（4メソッド） | BigQuery等への定期エクスポート |

### レポートクエリサービス設計

サービス側が実装する `QueryService`:

| メソッド | 説明 |
|---------|------|
| `GetDashboardSummary(ctx, asOf)` | ダッシュボードサマリー（MRR, ActiveContracts, Revenue, Outstanding, ChurnRate, MRRTrend） |
| `GetTrendData(ctx, metric, from, to, granularity)` | 時系列トレンドデータ |
| `GetContractReport(ctx, req)` | 契約レポート（ステータス別・タイプ別集計） |
| `GetRevenueReport(ctx, req)` | 収益レポート（MRR推移分析 = MRRMovementAnalysis 含む） |
| `GetCohortReport(ctx, req)` | コホートレポート（月別コホートの残存率推移） |

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

| フィールド群 | 主要フィールド | 説明 |
|-------------|---------------|------|
| 基本情報 | InvoiceID, InvoiceNumber, Status | 請求書識別 |
| 適格請求書対応 | `IsQualifiedInvoice`, `RegistrationNumber`（T+13桁） | インボイス制度対応 |
| 発行者情報 | IssuerInfo（Name, RegistrationNumber, Address等） | 適格請求書発行事業者情報 |
| 請求先情報 | BillToInfo（Name, Address, Email等） | 請求先 |
| 明細行 | LineItems（Description, Quantity: `float64`, Unit, UnitPrice, Amount, TaxRate, Period） | Quantity は float64（0.5時間等の表現用） |
| 税率別内訳 | `TaxSummary []TaxSummaryItem` | インボイス制度の必須要件 |
| 金額 | Subtotal, DiscountAmount, TaxAmount, Total, AppliedCredit, AmountDue | |
| 期間・日付 | BillingPeriod, IssueDate, DueDate | |
| 支払い情報 | PaymentInfo（Method, BankName, AccountNumber等） | 振込先情報 |

#### TaxSummaryItem（税率別内訳 = インボイス制度対応）

| フィールド | 説明 |
|-----------|------|
| `TaxRate` | 税率（`*big.Rat`） |
| `RateType` | `standard`（標準税率）/ `reduced`（軽減税率） |
| `TaxableAmount` | 課税対象額 |
| `TaxAmount` | 税額 |

> **設計意図**: 日本のインボイス制度（適格請求書等保存方式、2023年10月施行）では、
> 税率ごとの消費税額の明記が必須。`TaxSummaryItem` はこの要件を満たすための型。

### レンダリングオプション

| オプション | 説明 |
|-----------|------|
| `Format` | `pdf` / `html` / `json` / `xml`（電子インボイス用） |
| `Template` | テンプレート名 |
| `Locale` / `TimeZone` | 地域化設定 |
| `PaperSize` | `A4` / `Letter` / `B5` |

### 送付方法（DeliveryMethod）

| メソッド | 説明 |
|---------|------|
| `email` | メール送付 |
| `post` | 郵送 |
| `fax` | FAX |
| `download` | ダウンロードリンク |
| `api` | 外部システム連携 |

### InvoiceStorage Adapter（電子帳簿保存法対応）

請求書PDFの保管と検索を担当。改正電子帳簿保存法（電子取引データ保存義務化）に対応する設計。

| メソッド | 説明 |
|---------|------|
| `Store(ctx, req)` | 請求書を保管。SHA-256ハッシュとタイムスタンプを付与 |
| `Retrieve(ctx, invoiceID)` | 保管済み請求書を取得（改ざんチェック結果 `Verified` フラグ付き） |
| `Search(ctx, query)` | 電子帳簿保存法の3必須検索項目で検索 |
| `GetAuditLog(ctx, invoiceID)` | 監査ログ取得（created/viewed/downloaded/sent） |

#### 電子帳簿保存法の3必須検索項目

| 検索項目 | フィールド | 説明 |
|---------|-----------|------|
| 取引年月日 | `DateFrom` / `DateTo` | 請求書の発行日範囲 |
| 取引金額 | `AmountFrom` / `AmountTo` | 請求金額範囲 |
| 取引先 | `CounterpartyName` | 請求先名（部分一致） |

> **設計意図**: 電子帳簿保存法では電子取引データの保存時に「真実性の確保」（タイムスタンプ + ハッシュ）と
> 「検索機能の確保」（上記3項目での検索）が義務付けられている。`InvoiceStorage` はこの要件を構造的に保証する。

### サービス側の実装

- `InvoiceRenderer`: PDF/HTML レンダリング（wkhtmltopdf, chromedp, gotenberg等）
- `InvoiceDelivery`: メール送付（SendGrid, SES等）
- `InvoiceStorage`: PDF保存（S3, GCS等。電子帳簿保存法対応）

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
