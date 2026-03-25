# Contract Billing Core

契約・決済ドメインのOSSパッケージ。イベントソーシングとプラグインアーキテクチャを採用し、SaaS/サブスクリプションビジネスに必要な機能を提供します。

## 特徴

- **イベントソーシング** - 全操作の完全な監査証跡、任意時点の状態再構築
- **プラグインアーキテクチャ** - クーポン、税計算、通知などを柔軟に拡張
- **複数の契約タイプ** - 買い切り、サブスクリプション、従量課金に対応
- **ドメイン駆動設計** - 契約・請求書・支払いの明確なドメインモデル

## クイックスタート

```bash
go get github.com/contract-to-cash/core
```

詳細な導入手順は[利用ガイド](docs/guides/usage-guide.md)を参照してください。

## ドキュメント

### アーキテクチャ

- **[アーキテクチャ概要](docs/architecture.md)** - システム全体の構成と設計原則

### 設計詳細

| ドキュメント | 内容 |
|-------------|------|
| [ドメインモデル](docs/design/domain-model.md) | エンティティ、値オブジェクト、リポジトリの詳細 |
| [イベントソーシング](docs/design/event-sourcing.md) | Event Store、時点再構築、Projection |
| [プラグインシステム](docs/design/plugin-system.md) | フック、レジストリ、カスタムプラグイン作成 |
| [決済ゲートウェイ](docs/design/payment-gateway.md) | 決済インターフェース、Webhook処理 |
| [メトリクス・インボイス生成](docs/design/metrics-invoicegen.md) | KPI収集、請求書生成・送付 |

### 意思決定・ガイド

| ドキュメント | 内容 |
|-------------|------|
| [設計決定事項](docs/decisions/design-decisions.md) | 設計上の判断とその理由 |
| [利用ガイド](docs/guides/usage-guide.md) | サービスへの導入方法 |

## ディレクトリ構成

```
docs/
├── architecture.md              # アーキテクチャ概要
├── design/                      # 設計詳細
│   ├── domain-model.md          # ドメインモデル設計
│   ├── event-sourcing.md        # イベントソーシング設計
│   ├── plugin-system.md         # プラグインシステム設計
│   ├── payment-gateway.md       # 決済ゲートウェイ設計
│   └── metrics-invoicegen.md    # メトリクス・インボイス生成設計
├── decisions/                   # 設計決定記録
│   └── design-decisions.md
└── guides/                      # 利用ガイド
    └── usage-guide.md
```

## 主要ドメイン

| ドメイン | 説明 |
|---------|------|
| **Contract** | 契約のライフサイクル管理（作成、有効化、一時停止、解約、更新） |
| **Invoice** | 請求書の生成、発行、支払い記録 |
| **Payment** | 決済処理、返金 |
| **Usage** | 従量課金のメトリクス記録・集計 |

## 契約タイプ

| タイプ | 説明 |
|--------|------|
| `one_time` | 買い切り |
| `subscription` | サブスクリプション（定期課金） |
| `usage_based` | 従量課金 |

## プラグイン

### 公式プラグイン

- **Coupon** - クーポン・割引
- **Tax** - 税計算

### 拡張ポイント

| フック | 用途 |
|--------|------|
| `DiscountHook` | 割引計算（クーポン、ボリューム割引等） |
| `TaxHook` | 税計算（割引後の金額に対して実行） |
| `InvoiceLifecycleHook` | 請求書計算の前後処理 |
| `ContractLifecycleHook` | 契約ライフサイクルへの介入 |
| `PaymentHook` | 支払い処理への介入 |
| `MetricsHook` | メトリクス収集 |
| `InvoiceGenerationHook` | 請求書生成・送付 |

## ライセンス

[LICENSE](LICENSE)ファイルを参照してください。
