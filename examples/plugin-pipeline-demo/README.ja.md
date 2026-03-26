# プラグインパイプラインデモ

複数のプラグインが優先度ベースの請求計算パイプラインを構成する様子を示します。スクラッチで書いた2つのカスタムプラグインを含みます。

## 実行結果

```
  登録済みプラグイン:
    [Priority 0]   audit-log        (InvoiceLifecycleHook)
    [Priority 500] coupon            (DiscountHook)
    [Priority 500] loyalty-discount  (DiscountHook)
    [Priority 900] tax               (TaxHook)

  >> [AuditLog] BeforeCalculation: ContractID=...
  >> [Coupon] Usage recorded for coupon SAVE10
  >> [Loyalty] 5% discount on ¥10000 = -¥500
  >> [AuditLog] AfterCalculation: Total=¥9350, Discounts=2 applied

  小計:              ¥10,000
  割引 (クーポン+ロイヤリティ): -¥1,500
  割引後:           ¥8,500
  税 (10%):         +¥850
  合計:             ¥9,350
```

## このデモのプラグイン

| プラグイン | タイプ | 優先度 | 動作 |
|-----------|-------|-------|------|
| **AuditLogPlugin** (カスタム) | InvoiceLifecycleHook | 0 (最高) | 計算前後をログ出力 |
| **CouponPlugin** (組み込み) | DiscountHook | 500 | 10%クーポン「SAVE10」適用 |
| **LoyaltyDiscountPlugin** (カスタム) | DiscountHook | 500 | 5%ロイヤリティ割引 |
| **TaxPlugin** (組み込み) | TaxHook | 900 (低) | 消費税10% |

## 主要コンセプト

| コンセプト | 説明 |
|-----------|------|
| **優先度制御** | 数字が小さいほど先に実行。税金は設計上、割引の後に実行される |
| **フック合成** | 複数のDiscountHookが積み重なり、各プラグインは元の小計を参照 |
| **カスタムプラグイン** | インターフェースを実装するだけ。登録のボイラープレート不要 |
| **CalculationContext** | パイプラインを通じて渡される型安全なコンテキスト |

## カスタムプラグインの書き方

```go
type MyPlugin struct{ priority int }

func (p *MyPlugin) Name() string    { return "my-plugin" }
func (p *MyPlugin) Version() string { return "1.0.0" }
func (p *MyPlugin) Priority() int   { return p.priority }
func (p *MyPlugin) Initialize(_ context.Context, config plugin.Config) error { return nil }
func (p *MyPlugin) Shutdown(_ context.Context) error { return nil }

// 1つ以上のフックインターフェースを実装:
func (p *MyPlugin) CalculateDiscount(ctx *plugin.CalculationContext) (shared.Money, error) {
    // ロジックをここに
}
```

## 実行

```bash
go run ./examples/plugin-pipeline-demo/
```
