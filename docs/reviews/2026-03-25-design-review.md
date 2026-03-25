# Contract Billing Core 設計レビュー報告書

**レビュー日**: 2026-03-25
**対象ブランチ**: `docs/design-review-improvements`
**対象コミット**: 最新（改善1,3先行適用後）

---

## 総合評価

| 観点 | 評価 | 備考 |
|------|------|------|
| DDD適用の一貫性 | ⭐⭐⭐⭐⭐ | 層の分離が明確、依存方向が正しい。Gateway IF をポート層に移動済み |
| CQRS/イベントソーシング | ⭐⭐⭐⭐⭐ | 型付きイベント（DomainEvent IF）、EventRegistry、Clock IF 適用済み |
| 設計決定記録（ADR） | ⭐⭐⭐⭐⭐ | 代替案の検討が充実 |
| ドメインモデル | ⭐⭐⭐⭐⭐ | 共有ID型で循環依存解消、状態遷移ルール明記、Money型安全化 |
| プラグインシステム | ⭐⭐⭐⭐⭐ | 全フックISP準拠、計算順序コア保証、従量課金・ハイブリッド対応 |
| 決済ゲートウェイ | ⭐⭐⭐⭐⭐ | PCI DSS対応済み、Webhook堅牢化済み、ドメイン/ポート層分離完了 |
| メトリクス・請求書生成 | ⭐⭐⭐⭐ | KPI網羅性は高い。バッチ信頼性・リカバリ定義が不足 |
| セキュリティ | ⭐⭐⭐ | PCI DSS対応済み。監査ログ改竄対策、アクセス制御はまだ欠落 |
| パフォーマンス | ⭐⭐⭐ | スナップショット戦略はあるが具体的SLAなし |
| 改善計画の適用状況 | ⭐⭐⭐⭐⭐ | 改善1,2,3,4全て先行適用済み。改善5は実装時に適用 |

**全体スコア: 4.5 / 5.0** (初回 3.7 → P0対応後 4.0 → 設計課題対応+改善4適用後 4.3 → 改善1,3適用後 4.5)

---

## 改善計画の適用状況

| 改善 | 内容 | 状態 |
|------|------|------|
| **改善1** | ドメイン層とインフラ層の境界修正（Gateway→application/port/） | ✅ **先行適用済み** |
| **改善2** | プラグインフック分離（DiscountHook/TaxHook/ISP全面適用） | ✅ **先行適用済み** |
| **改善3** | イベントソーシング改善（型付きイベント、Clock IF） | ✅ **先行適用済み** |
| **改善4** | パッケージ構成と循環依存の解消（共有ID型、Engine削除） | ✅ **先行適用済み** |
| **改善5** | ドメインエラーの構造化 | 実装時に適用 |

---

## 対応済み事項一覧

### P0: セキュリティ・堅牢性（全て対応済み）

| # | 対応 | 詳細 |
|---|------|------|
| P0-1 | ✅ PCI DSS対応 | CardSource完全削除、トークン専用の非通過型設計、改正割賦販売法準拠 |
| P0-2 | ✅ Webhook堅牢性 | 双方向タイムスタンプ検証、TTL付き重複検出(72h)、DLQ、エラー分類、HTTPマッピング |
| P0-3 | ✅ API互換性 | SemVer方針、破壊的変更定義、カスタムプラグイン開発者向けガイド |

### 設計課題（全て対応済み）

| # | 対応 | 詳細 |
|---|------|------|
| 1 | ✅ 従量課金フロー | calculateUsageCharge()、含有枠差引、Graduated/Volume段階料金 |
| 2 | ✅ ハイブリッド課金 | 基本料金(BasePrice) + 従量料金を初版からサポート |
| 3 | ✅ 割引上限ガード | totalDiscount > subtotalの場合にsubtotalでcap |
| 4 | ✅ 請求書ステータスフロー | draft→finalized→issued→paid、GracePeriod、BillingConfig |
| 5 | ✅ Contract状態遷移 | past_due追加、全遷移ルール明記（suspended→cancelled等） |
| 6 | ✅ Payment状態遷移 | partially_refunded/charged_back追加、遷移ルール明記 |
| 7 | ✅ Money型安全化 | GreaterThan/IsZero/Zero追加、nilポインタ安全 |
| 8 | ✅ ISP全面適用 | ContractLifecycleHook→7個、PaymentHook→4個、MetricsHook→3個に分離 |
| 9 | ✅ 横断整合性修正 | Event構造体、GatewayRouter比較、フック呼び出し、metrics-invoicegen統一 |

### 改善計画の先行適用（全て対応済み）

| # | 対応 | 詳細 |
|---|------|------|
| 改善1 | ✅ ドメイン/インフラ境界修正 | Gateway IF→application/port/、DefaultGatewayRouter→infrastructure/gateway/、RawResponse削除 |
| 改善2 | ✅ フック分離 | InvoiceCalculationHook廃止、DiscountHook/TaxHook/InvoiceLifecycleHookに分離 |
| 改善3 | ✅ イベントソーシング改善 | DomainEvent IF・EventType定数・EventRegistry導入、型スイッチApply、Clock IF注入（time.Now()全廃止） |
| 改善4 | ✅ 循環依存解消 | shared/identifier.goに全ID型集約、Engine削除、importパス全統一 |

---

## 残存事項

### 実装フェーズで対応（Suggestion）

| # | 内容 |
|---|------|
| S1 | TieredPrice.calculateGraduated/calculateVolumeの具体実装 |
| S2 | LineItem.quantityの型統一（int vs float64） |
| S3 | DunningConfigの保持場所の決定 |
| S4 | ProrationConfigのContract含有 |
| S5 | Registry.Unregister()の提供 |
| S6 | BatchProcessorの設計 |
| S7 | Invoice voided/refundedの遷移条件 |
| S8 | Projection更新失敗時のリカバリ |
| S9 | Stripe実装例のMoney→int64変換 |

### 未着手の横断的課題

| # | 内容 |
|---|------|
| 1 | セキュリティアーキテクチャドキュメント（監査ログ改竄対策、PII暗号化） |
| 2 | パフォーマンスSLA（イベント再生速度、クエリ応答時間） |
| 3 | HA/DR設計（Event Store冗長化、RPO/RTO） |
| 4 | CQRS整合性ガイド（Projection更新の一貫性保証） |
| 5 | マルチテナントID戦略 |
