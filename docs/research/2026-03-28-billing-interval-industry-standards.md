# 技術調査レポート: サブスクリプション Billing Interval の業界標準

**調査日**: 2026-03-28
**トピック数**: 7 (Stripe, Chargebee, Recurly, Zuora, Kill Bill, Lago, Paddle)

---

## 比較サマリー

| プラットフォーム | データモデル | サポート間隔 | カスタム間隔 | フィールド名 |
|---|---|---|---|---|
| **Stripe** | `interval` (enum) + `interval_count` (int) | day/week/month/year | interval_count で任意倍数 | `recurring.interval`, `recurring.interval_count` |
| **Chargebee** | `period_unit` (enum) + `period` (int) | day/week/month/year | period で任意倍数 (要サイト設定) | `item_price.period_unit`, `item_price.period` |
| **Recurly** | `interval_unit` (enum) + `interval_length` (int) | days/months | interval_length で任意倍数 | `plan.interval_unit`, `plan.interval_length` |
| **Zuora** | `billingPeriod` (enum) + `specificBillingPeriod` (int) | 固定enum多数 + Specific_Months | Specific_Months で任意月数 | `ProductRatePlanCharge.billingPeriod` |
| **Kill Bill** | `billingPeriod` (enum) のみ | 17種の固定enum | なし (enumのみ) | `PhasePrice.billingPeriod` |
| **Lago** | `interval` (enum) のみ | weekly/monthly/quarterly/yearly | なし (enumのみ) | `plan.interval` |
| **Paddle** | `interval` (enum) + `frequency` (int) | day/week/month/year | frequency で任意倍数 | `billing_cycle.interval`, `billing_cycle.frequency` |

### 設計パターン分類

| パターン | 採用プラットフォーム | 柔軟性 |
|---|---|---|
| **A: interval + count** | Stripe, Chargebee, Recurly, Paddle | 高 (任意の倍数を表現可) |
| **B: 固定enum のみ** | Kill Bill, Lago | 低 (事前定義のみ) |
| **C: ハイブリッド** | Zuora | 中〜高 (固定enum + Specific_Months) |

---

## 1. Stripe

### 概要
Stripe は `Price` オブジェクトの `recurring` フィールド内に `interval` と `interval_count` の組み合わせで請求間隔を表現する。最もシンプルかつ広く採用されているモデル。2025年以降の Flexible Billing Mode では、1つの Subscription 内に異なる interval を持つ Price を混在させることも可能になった。

### データモデル

```json
{
  "id": "price_xxx",
  "object": "price",
  "recurring": {
    "interval": "month",
    "interval_count": 1,
    "trial_period_days": null,
    "usage_type": "licensed"
  }
}
```

**`recurring.interval`** (enum):
- `day`
- `week`
- `month`
- `year`

**`recurring.interval_count`** (integer):
- 1〜無制限 (例: `interval: "month"`, `interval_count: 3` = 四半期)

### カスタム間隔
`interval_count` により任意の倍数を指定可能。例:
- 2週間ごと: `interval: "week"`, `interval_count: 2`
- 四半期: `interval: "month"`, `interval_count: 3`
- 半年: `interval: "month"`, `interval_count: 6`

### 間隔変更時の挙動
- Price の interval は**作成後に変更不可** (immutable)
- 間隔を変更するには、新しい Price を作成し Subscription を更新する
- Mixed Interval Subscription (Flexible Billing Mode) では、最短 interval の倍数である必要がある

### コード例

```typescript
// 四半期プランの Price を作成
const price = await stripe.prices.create({
  product: 'prod_xxx',
  unit_amount: 3000,
  currency: 'jpy',
  recurring: {
    interval: 'month',
    interval_count: 3,
  },
});

// Subscription の interval を変更 (新Price へ切替)
await stripe.subscriptions.update('sub_xxx', {
  items: [{
    id: 'si_xxx',
    price: 'price_new_quarterly',
  }],
  proration_behavior: 'create_prorations',
});
```

### 参考URL
- [The Price object | Stripe API Reference](https://docs.stripe.com/api/prices/object)
- [Mixed interval subscriptions | Stripe Documentation](https://docs.stripe.com/billing/subscriptions/mixed-interval)
- [How products and prices work | Stripe Documentation](https://docs.stripe.com/products-prices/how-products-and-prices-work)

---

## 2. Chargebee

### 概要
Chargebee は Product Catalog 2.0 で `item_price` オブジェクトに `period_unit` と `period` の組み合わせで請求間隔を定義する。Stripe と類似の interval + count パターンだが、使用可能な組み合わせはサイト設定で事前に許可する必要がある。

### データモデル

```json
{
  "id": "basic-monthly",
  "item_id": "basic",
  "item_type": "plan",
  "period": 1,
  "period_unit": "month",
  "billing_cycles": 12
}
```

**`period_unit`** (enum):
- `day`
- `week`
- `month`
- `year`

**`period`** (integer):
- 任意の正の整数 (例: `period: 6`, `period_unit: "month"` = 半年)

**`billing_cycles`** (integer, optional):
- サブスクリプションの有効な請求サイクル数 (ライフタイム制御)

### カスタム間隔
`period` + `period_unit` の組み合わせで表現可能だが、**サイトの Billing Frequency 設定に事前登録が必要**。未登録の組み合わせはAPIエラーになる。

### 間隔変更時の挙動
- Plan レベルとSubscription レベルの両方で billing cycle を設定可能
- Subscription レベルの設定が Plan レベルをオーバーライドする
- 間隔変更は「Change Subscription」操作で新しい item_price に切り替える

### コード例

```python
# Chargebee で6ヶ月プランの item_price を作成
result = chargebee.ItemPrice.create({
    "id": "basic-6monthly",
    "item_id": "basic",
    "name": "Basic 6-Monthly",
    "pricing_model": "flat_fee",
    "price": 5000,
    "period": 6,
    "period_unit": "month",
    "currency_code": "JPY"
})
```

### 参考URL
- [Item prices | Chargebee API documentation](https://apidocs.chargebee.com/docs/api/item_prices)
- [Billing Cycle for Addons - Chargebee Docs](https://www.chargebee.com/docs/billing/2.0/subscriptions/addons-billingcycle)
- [Subscription Lifetime - Chargebee Docs](https://www.chargebee.com/docs/2.0/subscription-life-time.html)

---

## 3. Recurly

### 概要
Recurly は `Plan` オブジェクトに `interval_unit` と `interval_length` で請求間隔を定義する。Stripe/Chargebee と同じ interval + count パターンだが、interval_unit は `days` と `months` の2値のみ。**Plan 作成後に interval は変更不可**という強い制約がある。

### データモデル

```json
{
  "id": "plan_xxx",
  "code": "basic-monthly",
  "interval_unit": "months",
  "interval_length": 1,
  "trial_unit": "days",
  "trial_length": 14
}
```

**`interval_unit`** (enum):
- `days`
- `months`

**`interval_length`** (integer):
- 任意の正の整数

### カスタム間隔
`interval_length` で倍数指定可能:
- 週次: `interval_unit: "days"`, `interval_length: 7`
- 四半期: `interval_unit: "months"`, `interval_length: 3`
- 年次: `interval_unit: "months"`, `interval_length: 12`

**注意**: `weeks` や `years` という interval_unit は存在しない。days と months の組み合わせですべてを表現する。

### 間隔変更時の挙動
- **Plan の interval_unit と interval_length は作成後に変更不可** (immutable)
- 間隔変更には新しい Plan を作成し、Subscription Change で切り替える
- 変更のタイミングは `immediately`, `at_renewal`, `at_term_end` から選択可能

### コード例

```ruby
# Recurly で四半期プランを作成
plan = Recurly::Plan.create(
  code: 'basic-quarterly',
  name: 'Basic Quarterly',
  interval_unit: 'months',
  interval_length: 3,
  currencies: [{
    currency: 'JPY',
    unit_amount: 3000
  }]
)
```

### 参考URL
- [Plans | Recurly Documentation](https://docs.recurly.com/docs/plans)
- [Subscription billing terms | Recurly](https://docs.recurly.com/docs/subscription-terms)
- [How Do I Set a Long-Term Subscription with a Monthly Billing Cycle?](https://support.recurly.com/hc/en-us/articles/360027378932)

---

## 4. Zuora

### 概要
Zuora は エンタープライズ向けの最も柔軟な設計を持ち、`ProductRatePlanCharge` の `billingPeriod` フィールドに豊富な固定 enum 値を提供する。さらに `Specific_Months` / `Specific_Weeks` を使えば任意の月数・週数も指定可能。Billing Period のカスタマイズはテナント設定で有効化する。

### データモデル

```json
{
  "ProductRatePlanCharge": {
    "billingPeriod": "Quarter",
    "billingTiming": "IN_ADVANCE",
    "billCycleType": "DefaultFromCustomer",
    "billingPeriodAlignment": "AlignToCharge",
    "specificBillingPeriod": null
  }
}
```

**`billingPeriod`** (enum):
- `Month`
- `Quarter`
- `Semi_Annual`
- `Annual`
- `Eighteen_Months`
- `Two_Years`
- `Three_Years`
- `Five_Years`
- `Specific_Months` (任意月数、`specificBillingPeriod` で指定)
- `Week`
- `Specific_Weeks` (任意週数、`specificBillingPeriod` で指定)
- `Subscription_Term`

**`specificBillingPeriod`** (integer):
- `Specific_Months` / `Specific_Weeks` 選択時に月数・週数を指定

### カスタム間隔
`Specific_Months` + `specificBillingPeriod` で任意の月数を指定可能。例:
- 4ヶ月: `billingPeriod: "Specific_Months"`, `specificBillingPeriod: 4`
- 2週間: `billingPeriod: "Specific_Weeks"`, `specificBillingPeriod: 2`

### 間隔変更時の挙動
- Orders API を使い Subscription の Charge を変更可能
- Amendment (修正) として処理され、日割り計算やクレジットが自動適用される
- `billCycleDay` の変更も可能で、既請求分との調整ロジックが複雑

### コード例

```json
// Zuora REST API: 4ヶ月ごとの Recurring Charge を作成
{
  "ProductRatePlanCharge": {
    "Name": "Custom 4-Month Plan",
    "BillingPeriod": "Specific_Months",
    "SpecificBillingPeriod": 4,
    "BillingTiming": "IN_ADVANCE",
    "ChargeModel": "FlatFee",
    "ChargeType": "Recurring",
    "TriggerEvent": "ContractEffective"
  }
}
```

### 参考URL
- [Billing periods customization | Zuora Product Documentation](https://docs.zuora.com/en/zuora-billing/set-up-zuora-billing/billing-settings-configuration/general-billing-settings/billing-periods-customization)
- [CRUD: Create a product rate plan charge | Zuora API](https://developer.zuora.com/v1-api-reference/api/operation/Object_POSTProductRatePlanCharge/)
- [Billing timing | Zuora Product Documentation](https://docs.zuora.com/en/zuora-billing/set-up-zuora-billing/build-product-and-prices/basic-concepts-and-terms/billing-timing)

---

## 5. Kill Bill

### 概要
Kill Bill はオープンソースの Billing プラットフォームで、Java の enum として `BillingPeriod` を定義する。**業界で最も豊富な固定 enum** を持ち、17種類の請求間隔をサポートする。ただしカスタム間隔 (任意の倍数指定) はサポートしない。Catalog XML で Plan の billingPeriod を定義する。

### データモデル

```java
// org.killbill.billing.catalog.api.BillingPeriod
public enum BillingPeriod {
    DAILY,
    WEEKLY,
    BIWEEKLY,
    THIRTY_DAYS,
    THIRTY_ONE_DAYS,
    SIXTY_DAYS,
    NINETY_DAYS,
    MONTHLY,
    BIMESTRIAL,      // 2ヶ月
    QUARTERLY,        // 3ヶ月
    TRIANNUAL,        // 4ヶ月 (年3回)
    BIANNUAL,         // 6ヶ月 (年2回)
    ANNUAL,
    SESQUIENNIAL,     // 18ヶ月
    BIENNIAL,         // 2年
    TRIENNIAL,        // 3年
    NO_BILLING_PERIOD
}
```

### カスタム間隔
**サポートなし**。上記の固定 enum のみ。5ヶ月や7ヶ月といった間隔が必要な場合は対応できない。

### 間隔変更時の挙動
- Catalog のバージョニングにより、新しい Plan を追加して Change Plan API で切り替え
- Phase (Trial -> Evergreen 等) で billingPeriod を変更する設計が推奨
- 既存 Subscription の billingPeriod は Plan 変更を通じて間接的に変わる

### コード例

```xml
<!-- Kill Bill Catalog XML -->
<plan name="basic-monthly">
  <finalPhase type="EVERGREEN">
    <duration>
      <unit>UNLIMITED</unit>
    </duration>
    <recurring>
      <billingPeriod>MONTHLY</billingPeriod>
      <recurringPrice>
        <price>
          <currency>JPY</currency>
          <value>1000</value>
        </price>
      </recurringPrice>
    </recurring>
  </finalPhase>
</plan>
```

### 参考URL
- [BillingPeriod.java | GitHub](https://github.com/killbill/killbill-api/blob/master/src/main/java/org/killbill/billing/catalog/api/BillingPeriod.java)
- [Kill Bill subscription guide](https://docs.killbill.io/latest/userguide_subscription)
- [Kill Bill Glossary](https://docs.killbill.io/0.24/Kill-Bill-Glossary)

---

## 6. Lago

### 概要
Lago はオープンソースの Usage-Based Billing プラットフォームで、`Plan` オブジェクトの `interval` フィールドに固定 enum で請求間隔を定義する。シンプルだがカスタム間隔はサポートしない。quarterly と semiannual は比較的最近追加された。

### データモデル

```json
{
  "plan": {
    "name": "Basic",
    "code": "basic",
    "interval": "monthly",
    "pay_in_advance": true,
    "amount_cents": 1000,
    "amount_currency": "JPY"
  }
}
```

**`interval`** (enum):
- `weekly`
- `monthly`
- `quarterly` (3ヶ月、v0.44.1-beta 以降)
- `semiannual` (6ヶ月)
- `yearly`

### カスタム間隔
**サポートなし**。上記5つの固定 enum のみ。2ヶ月、4ヶ月、2年といった間隔は表現できない。

### 間隔変更時の挙動
- Subscription を新しい Plan に切り替えることで間隔変更
- `billing_time` で `anniversary` (作成日基準) と `calendar` (暦基準) を選択可能
- yearly プランでも charges を monthly で計算するオプションがある

### コード例

```bash
# Lago API: 四半期プランを作成
curl -X POST https://api.getlago.com/api/v1/plans \
  -H "Authorization: Bearer $LAGO_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "plan": {
      "name": "Basic Quarterly",
      "code": "basic-quarterly",
      "interval": "quarterly",
      "pay_in_advance": true,
      "amount_cents": 3000,
      "amount_currency": "JPY"
    }
  }'
```

### 参考URL
- [The plan object - Lago](https://getlago.com/docs/api-reference/plans/object)
- [Plan model - Lago](https://getlago.com/docs/guide/plans/plan-model)
- [GitHub - getlago/lago](https://github.com/getlago/lago)

---

## 7. Paddle

### 概要
Paddle は `Price` オブジェクトの `billing_cycle` フィールドに `interval` と `frequency` の組み合わせで請求間隔を定義する。Stripe と同じ interval + count パターンだが、フィールド名が `frequency` である点が異なる。

### データモデル

```json
{
  "id": "pri_xxx",
  "product_id": "pro_xxx",
  "billing_cycle": {
    "interval": "month",
    "frequency": 1
  },
  "trial_period": {
    "interval": "day",
    "frequency": 14
  }
}
```

**`billing_cycle.interval`** (enum):
- `day`
- `week`
- `month`
- `year`

**`billing_cycle.frequency`** (integer):
- 1 以上の正の整数

### カスタム間隔
`frequency` で任意の倍数を指定可能:
- 2週間ごと: `interval: "week"`, `frequency: 2`
- 四半期: `interval: "month"`, `frequency: 3`
- 2年: `interval: "year"`, `frequency: 2`

### 間隔変更時の挙動
- Price の billing_cycle は作成後に変更不可
- 新しい Price を作成し、Subscription を更新して切り替える
- 請求日 (billing date) の変更は `pause` -> `resume` またはスケジュール変更で対応

### コード例

```typescript
// Paddle API: 四半期プランの Price を作成
const price = await paddle.prices.create({
  productId: 'pro_xxx',
  description: 'Basic Quarterly',
  unitPrice: {
    amount: '3000',
    currencyCode: 'JPY',
  },
  billingCycle: {
    interval: 'month',
    frequency: 3,
  },
});
```

### 参考URL
- [Create a price - Paddle Developer](https://developer.paddle.com/api-reference/prices/create-price)
- [Change billing dates - Paddle Developer](https://developer.paddle.com/build/subscriptions/change-billing-dates)
- [Create products and prices - Paddle Developer](https://developer.paddle.com/build/products/create-products-prices)

---

## 設計上の示唆

### 1. 業界標準は「interval + count」パターン
大手プラットフォーム 7社中 4社 (Stripe, Chargebee, Recurly, Paddle) が `interval` (enum) + `count` (integer) の組み合わせを採用。これが事実上の業界標準。

### 2. 最小限の interval enum は 4値
`day`, `week`, `month`, `year` の4値が最も一般的。Recurly は `days` + `months` の2値に絞り、weeks/years は length で表現する設計。

### 3. enum のみの設計は制約が大きい
Kill Bill (17値) や Lago (5値) の固定 enum パターンは、新しい間隔が必要になるたびにコード変更が必要で拡張性に劣る。

### 4. 間隔変更は基本的に immutable 設計
全社共通で、Price/Plan の billing interval は**作成後に変更不可**。変更するには新しい Price/Plan を作成して Subscription を切り替える。これは按分計算や課金履歴の整合性を保つため。

### 5. 自プロジェクトへの推奨
`interval` (enum: `day` | `week` | `month` | `year`) + `intervalCount` (integer) のモデルが最も柔軟かつ業界慣行に沿った設計。
