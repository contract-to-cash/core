# 技術調査レポート: 請求書再発行 (Invoice Reissue / Regenerate) の業界標準

**調査日**: 2026-03-28
**トピック数**: 7 (Stripe, Chargebee, Recurly, Zuora, Lago, freee, Money Forward)

---

## 全体サマリー: 2つの主要パターン

請求書再発行の設計は大きく **2つのパターン** に分かれる。

| パターン | 概要 | 採用プラットフォーム |
|----------|------|----------------------|
| **Void-and-Recreate** | 元の請求書をVoidし、新しい請求書を発行 | Chargebee, Recurly, Lago |
| **Revision (Inline Edit)** | 元の請求書を改訂し、旧版をVoid + 新版を自動生成 | Stripe |
| **Reversal + Rebill** | Credit Memoで打消し、新請求書を再生成 | Zuora |
| **修正インボイス** | 日本インボイス制度準拠の修正版交付 | freee, Money Forward |

---

## 1. Stripe

### 概要
Stripeは **Revision パターン** を採用。finalized済みのInvoiceをインラインで編集すると、旧Invoiceが自動的にVoidされ、新しいInvoiceが生成される。`latest_revision` パラメータでリビジョンチェーンを追跡できる。

### ワークフロー

```
[Invoice (open/uncollectible)]
    |
    v  Update Invoice (API or Dashboard)
[Old Invoice] ---(auto void)---> status: void
    |
    v
[New Invoice] ---(auto finalize)---> status: open
    |
    latest_revision: new_inv_id (全リビジョンに伝播)
```

### API コード例

```bash
# 1. Invoiceを改訂(Revision) - Stripe Dashboard推奨だがAPIでも可能
# draft状態で改訂版を作成
curl https://api.stripe.com/v1/invoices/in_xxxOLD/update \
  -u sk_test_xxx: \
  -d "description=Revised invoice"

# 2. 単純なVoid → 手動で新規作成するパターン
# Step 2a: Void
curl https://api.stripe.com/v1/invoices/in_xxxOLD/void \
  -u sk_test_xxx: \
  -X POST

# Step 2b: 新規Invoice作成
curl https://api.stripe.com/v1/invoices \
  -u sk_test_xxx: \
  -d customer=cus_xxx \
  -d "metadata[original_invoice]=in_xxxOLD"

# Step 2c: Finalize
curl https://api.stripe.com/v1/invoices/in_xxxNEW/finalize \
  -u sk_test_xxx: \
  -X POST
```

### 採番ルール
- Revisionで新しいInvoiceが生成されると **新しいInvoice番号** が付与される
- 元のInvoice番号はVoid状態で保持される
- `latest_revision` パラメータで最新版を追跡可能

### 元請求書との紐付け
- `latest_revision`: 最新リビジョンのInvoice ID
- Dashboardの「History」セクションにリビジョンタイムラインが表示
- Voidされた請求書から新しい請求書へのリンクが自動的に作成

### 監査証跡
- Voidされた請求書は削除されず、`status: void` として保持
- レポート上はゼロ値として扱われる
- Event APIでvoid/finalizeイベントが記録される

### ベストプラクティス
- open/uncollectible状態のInvoiceにはRevisionパターンを使用
- paid/void状態のInvoiceは改訂不可 → Void + 新規作成
- Credit Noteも併用可能（部分返金の場合）
- customer/productフィールドはRevisionで変更不可

### 参考URL
- [Edit invoices | Stripe Documentation](https://docs.stripe.com/invoicing/invoice-edits)
- [Void an invoice | Stripe API Reference](https://docs.stripe.com/api/invoices/void)
- [Status transitions and finalization](https://docs.stripe.com/invoicing/integration/workflow-transitions)
- [The Invoice object](https://docs.stripe.com/api/invoices/object)

---

## 2. Chargebee

### 概要
Chargebeeは **Void-and-Regenerate パターン** を採用。誤った請求書をVoidした後、サブスクリプションに対してRegenerateを実行し、正しい内容で新しい請求書を生成する。

### ワークフロー

```
[Invoice (payment_due/paid/posted)]
    |
    v  Void Invoice
[Invoice] ---> status: voided
    |
    v  修正 (coupon追加、税率変更、住所変更等)
    |
    v  Regenerate Invoice (Subscription経由)
[New Invoice] ---> 新しいInvoice番号で発行
```

### API コード例

```bash
# 1. Invoice Void
curl https://{site}.chargebee.com/api/v2/invoices/{invoice_id}/void \
  -u {api_key}: \
  -X POST \
  -d "comment=Incorrect tax applied"

# 2. サブスクリプション情報を修正 (例: coupon追加)
curl https://{site}.chargebee.com/api/v2/subscriptions/{sub_id} \
  -u {api_key}: \
  -d "coupon_ids[0]=summer_sale"

# 3. Invoice Regenerate (サブスクリプション経由)
curl https://{site}.chargebee.com/api/v2/subscriptions/{sub_id}/regenerate_invoice \
  -u {api_key}: \
  -X POST

# 4. Invoice詳細の直接更新 (住所・VAT・PO番号)
curl https://{site}.chargebee.com/api/v2/invoices/{invoice_id}/update_details \
  -u {api_key}: \
  -d "billing_address[line1]=New Address" \
  -d "vat_number=EU123456789"
```

### 採番ルール
- Regenerate時は **新しいInvoice番号** が発行される
- 元のInvoice番号はVoid状態で保持

### 元請求書との紐付け
- Void理由のコメントで関連性を記録
- Credit Note（北欧圏向け）が自動生成されるオプションあり

### 監査証跡
- VoidされたInvoiceは履歴に残る
- Credit Noteがvoided invoiceに適用される記録
- Chargebee Activity Logで操作履歴を追跡可能

### 注意点
- Regenerateはサブスクリプションに紐づくInvoiceのみ対象
- One-off Invoiceの再発行は手動で新規作成が必要
- Credit Noteを使ったVoidは北欧諸国向けの特別対応（デフォルトでは無効、サポートへ連絡要）
- Full term / Custom Period（日割り）の選択が可能

### 参考URL
- [How do I void and regenerate an invoice?](https://www.chargebee.com/docs/2.0/invoices-credit-notes-and-quotes/articles-and-faq/how-do-i-void-and-regenerate-an-invoice.html)
- [Invoice Operations](https://www.chargebee.com/docs/2.0/invoice-operations.html)
- [Invoices API](https://apidocs.chargebee.com/docs/api/invoices)

---

## 3. Recurly

### 概要
Recurlyは **Void + Credit Invoice** パターンを採用。請求書のVoidに加え、Credit Invoice（Credit Memo）による返金・クレジット調整が充実している。「Reopen」機能は手動請求書の状態遷移に使用。

### ワークフロー

```
[Charge Invoice (paid/failed)]
    |
    +--> Refund Line Items --> [Credit Invoice生成]
    |                             |
    |                             v  返金 or アカウントクレジット
    |
    +--> Void (未払いInvoiceのみ) --> [Invoice: voided]

[Manual Invoice (paid/failed)]
    |
    +--> Reopen --> [Invoice: open] --> 再度支払い記録が可能
```

### API コード例

```bash
# 1. Invoice Void (Credit Invoice)
curl -X PUT https://v3.recurly.com/invoices/{invoice_id}/void \
  -H "Authorization: Basic {base64_api_key}" \
  -H "Accept: application/vnd.recurly.v2021-02-25"

# 2. Line Item Refund (部分返金 → 新Credit Invoice生成)
curl -X POST https://v3.recurly.com/invoices/{invoice_id}/refund \
  -H "Authorization: Basic {base64_api_key}" \
  -H "Accept: application/vnd.recurly.v2021-02-25" \
  -H "Content-Type: application/json" \
  -d '{
    "type": "line_items",
    "line_items": [
      {
        "id": "li_xxxx",
        "quantity": 1,
        "prorate": true
      }
    ]
  }'

# 3. Manual Invoice Reopen
curl -X PUT https://v3.recurly.com/invoices/{invoice_id}/reopen \
  -H "Authorization: Basic {base64_api_key}" \
  -H "Accept: application/vnd.recurly.v2021-02-25"
```

### 採番ルール
- Credit Invoiceは独自の番号が付与される
- 元のCharge Invoiceの番号は変更されない
- Voidされた場合も元の番号は保持される

### 元請求書との紐付け
- Credit InvoiceはCharge Invoiceへの参照を保持
- Credit Payment（クレジット適用）で会計上の紐付け

### 注意点
- 「Reopen」はManual Invoice専用（自動請求書には使えない）
- Void可能なのは未払い状態のInvoiceのみ
- 支払い済みInvoiceの修正にはRefund → 新Invoice作成が必要

### 参考URL
- [Invoice management](https://docs.recurly.com/recurly-subscriptions/docs/invoice-management)
- [Credit invoices](https://docs.recurly.com/recurly-subscriptions/docs/credit-invoices)
- [Refund/Void Line Items](https://dev.recurly.com/docs/line-item-refunds)

---

## 4. Zuora

### 概要
Zuoraは最も高機能な **Reversal + Rebill パターン** を採用。Invoice Reversalは単一操作で「Credit Memo生成 → 元Invoiceへの適用 → 課金日リセット → 再請求フラグ設定」を自動実行する。エンタープライズ向けに複雑な会計要件に対応。

### ワークフロー

```
[Posted Invoice (誤り発見)]
    |
    v  PUT /v1/invoices/{id}/reverse
[自動実行される処理]:
  1. Credit Memo 生成 (元Invoiceのミラー)
  2. Credit Memo を元Invoiceに適用
  3. Charge Through Date をリセット
  4. 元Invoiceに "reversed" フラグ設定
    |
    v  修正後、Bill Run を再実行
[New Invoice] ---> 正しい内容で再生成
```

### API コード例

```bash
# 1. Invoice Reversal
curl -X PUT https://rest.zuora.com/v1/invoices/{invoice_id}/reverse \
  -H "Authorization: Bearer {oauth_token}" \
  -H "Content-Type: application/json" \
  -d '{
    "applyEffectiveDate": "2026-03-28",
    "memo": "Incorrect pricing applied"
  }'

# 2. Bill Run で再請求 (修正後)
curl -X POST https://rest.zuora.com/v1/object/bill-run \
  -H "Authorization: Bearer {oauth_token}" \
  -H "Content-Type: application/json" \
  -d '{
    "invoiceDate": "2026-03-28",
    "targetDate": "2026-03-28",
    "accountId": "acc_xxxx"
  }'

# 3. Credit Memo の直接作成 (部分修正の場合)
curl -X POST https://rest.zuora.com/v1/creditmemos \
  -H "Authorization: Bearer {oauth_token}" \
  -H "Content-Type: application/json" \
  -d '{
    "invoiceId": "inv_xxxx",
    "items": [
      {
        "invoiceItemId": "ii_xxxx",
        "amount": 100.00,
        "comment": "Price correction"
      }
    ]
  }'
```

### 採番ルール
- Reversed Invoiceは元の番号を保持（reversedフラグが立つ）
- Credit Memoには独自のCM番号が付与される
- 再生成されたInvoiceには新しい番号が付与される

### 元請求書との紐付け
- Credit Memo → 元Invoice の参照リンク
- Reversed フラグによる状態管理
- Invoice Settlement機能で適用関係を追跡

### 監査証跡
- 全操作がTransaction Journalに記録される
- Zuora Revpro（Revenue Recognition）との連携
- Workflow機能で自動化・承認フローを構築可能

### 注意点
- Invoice Settlement機能の有効化が前提条件
- 2,000項目超のInvoiceは非同期処理となる
- E-Invoice（電子インボイス）のRegenerateは別機能（送信失敗時の再送信）

### 参考URL
- [Invoice reversal | Zuora](https://docs.zuora.com/en/zuora-billing/bill-your-customer/invoice-management/invoice-reversal)
- [Reverse an invoice API](https://developer.zuora.com/v1-api-reference/api/operation/PUT_ReverseInvoice/)
- [Credit Memos API](https://developer.zuora.com/v1-api-reference/api/credit-memos)

---

## 5. Lago (OSS)

### 概要
Lagoは **Void-and-Regenerate パターン** を採用。APIでVoidを実行し、UIから再生成（Regenerate）する設計。再生成時に元のline itemsが自動的にプリフィルされ、元の請求書との紐付けも自動で行われる。

### ワークフロー

```
[Invoice (finalized)]
    |
    v  POST /api/v1/invoices/{id}/void
[Invoice] ---> status: voided
    |  options: generate_credit_note, refund_amount, credit_amount
    |
    v  UI: Regenerate (voided invoiceの画面から)
[New Invoice] ---> 元のline items がプリフィル
                   元のvoided invoiceへのリンクが自動設定
```

### API コード例

```bash
# 1. Invoice Void (Credit Note生成 + 返金オプション付き)
curl -X POST https://api.getlago.com/api/v1/invoices/{lago_id}/void \
  -H "Authorization: Bearer {api_key}" \
  -H "Content-Type: application/json" \
  -d '{
    "generate_credit_note": true,
    "refund_amount": 0,
    "credit_amount": 10000
  }'

# 2. One-off Invoice 作成 (再生成の代替としてAPI経由)
curl -X POST https://api.getlago.com/api/v1/invoices \
  -H "Authorization: Bearer {api_key}" \
  -H "Content-Type: application/json" \
  -d '{
    "external_customer_id": "cust_001",
    "currency": "JPY",
    "fees": [
      {
        "add_on_code": "setup_fee",
        "units": 1,
        "unit_amount_cents": 10000,
        "description": "Re-issued: original invoice lago_id_xxx"
      }
    ]
  }'

# 3. Credit Note 作成 (部分修正の場合)
curl -X POST https://api.getlago.com/api/v1/credit_notes \
  -H "Authorization: Bearer {api_key}" \
  -H "Content-Type: application/json" \
  -d '{
    "invoice_id": "lago_id_xxx",
    "reason": "other",
    "items": [
      {
        "fee_id": "fee_xxx",
        "credit_amount_cents": 5000
      }
    ]
  }'
```

### 採番ルール
- 再生成されたInvoiceには **新しいInvoice番号** が付与される
- 元のInvoice番号はVoid状態で保持

### 元請求書との紐付け
- Regenerated Invoiceは自動的にOriginal Voided Invoiceへのリンクを保持
- Credit Noteが元Invoiceへの参照を保持

### 注意点
- Regenerate機能は **UIのみ** （2026年3月時点ではAPIエンドポイントなし）
- API経由での再発行はOne-off Invoice作成で代替
- Voided prepaid credit invoiceの再生成は新しいWallet Transactionで対応
- Regenerate時にサービス期間(service period)の変更は不可
- draft状態のInvoiceはVoid不可（まだfinalizeされていないため）

### 参考URL
- [Void invoices - Lago](https://docs.getlago.com/guide/invoicing/void)
- [Credit notes - Lago](https://docs.getlago.com/guide/credit-notes)
- [Lago GitHub](https://github.com/getlago/lago)

---

## 6. freee (日本の会計SaaS)

### 概要
freeeは日本のインボイス制度（適格請求書等保存方式）に完全対応した修正インボイス機能を提供。「修正版の全項目再交付」と「修正事項のみ記載した書類の交付」の2パターンをサポートする。

### ワークフロー (インボイス制度準拠)

```
[適格請求書 (発行済み)]
    |
    v  誤り発見
    |
    +---> パターンA: 全項目修正・再交付
    |     [修正版 適格請求書]
    |     - タイトルに「修正版」「修正済」を明記
    |     - 全ての記載事項を正しい内容で記載
    |     - 元の請求書との対応関係を明示
    |
    +---> パターンB: 修正事項のみ記載
          [修正インボイス(差分)]
          - 元の請求書番号を記載
          - 修正した事項のみを記載
          - 「何を」「どのように」修正したかを明示
```

### API (freee会計 API)

```bash
# freee請求書 API でインボイス制度対応の請求書を作成
# API v3 (2023年10月〜 インボイス制度対応)

# 1. 請求書の作成
curl -X POST https://api.freee.co.jp/api/1/invoices \
  -H "Authorization: Bearer {access_token}" \
  -H "Content-Type: application/json" \
  -d '{
    "company_id": 12345,
    "partner_id": 67890,
    "invoice_number": "INV-2026-0001-R1",
    "title": "【修正版】請求書",
    "invoice_date": "2026-03-28",
    "due_date": "2026-04-30",
    "invoice_layout": "default_classic",
    "tax_entry_method": "inclusive",
    "invoice_contents": [
      {
        "name": "サービス利用料（修正後）",
        "quantity": 1,
        "unit_price": 100000,
        "tax_rate": 10
      }
    ],
    "notes": "本請求書はINV-2026-0001の修正版です。金額を訂正いたしました。"
  }'

# 2. 請求書の取得
curl https://api.freee.co.jp/api/1/invoices/{invoice_id}?company_id=12345 \
  -H "Authorization: Bearer {access_token}"

# 3. 請求書の更新 (下書き状態のみ)
curl -X PUT https://api.freee.co.jp/api/1/invoices/{invoice_id} \
  -H "Authorization: Bearer {access_token}" \
  -H "Content-Type: application/json" \
  -d '{
    "company_id": 12345,
    "title": "【修正版】請求書"
  }'
```

### 採番ルール
- 日本の商慣行に基づき、修正版は **元の番号に枝番** を付けるのが一般的
  - 例: `INV-2026-0001` → `INV-2026-0001-R1`
- freeeのシステム上は新しい請求書として作成（番号は手動設定）
- 元の請求書は「発行前の状態に戻す」操作で取消可能

### 日本インボイス制度との関連
- **適格請求書発行事業者の義務**: 誤りのあるインボイスの修正版を交付する義務がある
- **買い手側**: 受領したインボイスに追記・修正を行うことは原則不可。発行事業者に再交付を依頼
- **保存義務**: 元のインボイスと修正版の両方を保存する必要がある
- **登録番号**: API経由で適格請求書発行事業者の登録番号(T + 13桁)を設定可能

### 参考URL
- [インボイスの訂正・再交付について | freee](https://support.freee.co.jp/hc/ja/articles/23501282256281)
- [freee会計 APIのインボイス制度対応](https://developer.freee.co.jp/news/6369)
- [freee請求書のインボイス制度対応機能](https://support.freee.co.jp/hc/ja/articles/23506259068825)

---

## 7. Money Forward (日本の会計SaaS)

### 概要
マネーフォワード クラウド請求書はインボイス制度に対応した請求書の作成・修正・再発行機能を提供。API v3.1.0以降でインボイス制度対応の請求書作成が可能。修正返還インボイスのテンプレートも用意されている。

### ワークフロー (インボイス制度準拠)

```
[適格請求書 (発行済み)]
    |
    v  誤り発見
    |
    +---> 方法1: 修正版の全項目再交付
    |     - 正しい内容で新しい請求書を作成
    |     - 「再発行」であることを明記
    |     - 元の請求書の破棄を取引先に通知
    |
    +---> 方法2: 修正事項のみ記載した書類
          - 修正返還インボイステンプレート使用
          - 元の請求書との関連性を明示
          - 差分のみを記載
```

### API コード例

```bash
# Money Forward クラウド請求書 API v3.1.0 (インボイス制度対応)

# 1. 請求書の作成 (修正版)
curl -X POST https://invoice.moneyforward.com/api/v3/billings \
  -H "Authorization: Bearer {access_token}" \
  -H "Content-Type: application/json" \
  -d '{
    "billing": {
      "partner_id": "partner_xxx",
      "department_id": "dept_xxx",
      "billing_number": "MF-2026-0001-R1",
      "title": "【再発行】請求書",
      "billing_date": "2026-03-28",
      "due_date": "2026-04-30",
      "note": "本請求書はMF-2026-0001の再発行版です。",
      "items": [
        {
          "name": "サービス利用料（修正後）",
          "quantity": 1,
          "unit_price": 100000,
          "tax_rate": 0.1
        }
      ]
    }
  }'

# 2. 請求書のステータス更新 (下書き → 未入金)
curl -X PATCH https://invoice.moneyforward.com/api/v3/billings/{billing_id} \
  -H "Authorization: Bearer {access_token}" \
  -H "Content-Type: application/json" \
  -d '{
    "billing": {
      "status": "unpaid"
    }
  }'

# 3. 請求書の取得
curl https://invoice.moneyforward.com/api/v3/billings/{billing_id} \
  -H "Authorization: Bearer {access_token}"
```

### 採番ルール
- 再発行時は新しい請求書番号を付与するのが推奨
- 元の番号に「-R1」等の枝番を追加する慣行
- システム上は任意の番号を設定可能

### 日本インボイス制度との関連
- API v3.1.0でインボイス制度対応の請求書作成が可能
- 修正返還インボイスのテンプレートを提供
- 元の請求書と修正版の両方を保存する機能
- 適格請求書に必要な全記載事項（登録番号、税率ごとの合計等）に対応

### 注意点
- API利用は有料プラン契約が前提（追加料金は不要）
- プランにより利用可能な機能に制限あり
- 郵送は別途有料サービス

### 参考URL
- [適格請求書は再発行できる？修正方法や保存について解説](https://biz.moneyforward.com/invoice/basic/60409/)
- [インボイス制度に対応した請求書の作成方法](https://biz.moneyforward.com/support/invoice/guide/document02/do018.html)
- [クラウド請求書APIについて](https://biz.moneyforward.com/support/invoice/guide/api-guide/a03.html)

---

## 横断比較表

| 項目 | Stripe | Chargebee | Recurly | Zuora | Lago | freee | Money Forward |
|------|--------|-----------|---------|-------|------|-------|---------------|
| **パターン** | Revision | Void+Regenerate | Void+Credit | Reversal+Rebill | Void+Regenerate | 修正インボイス | 修正インボイス |
| **新番号付与** | Yes | Yes | Yes (Credit) | Yes (新Invoice) | Yes | 手動設定 | 手動設定 |
| **元請求書リンク** | latest_revision | Comment | Credit Payment | Reversed flag | Auto link | 備考欄 | 備考欄 |
| **API対応** | Full | Full | Full | Full | Void only* | Full | Full |
| **Credit Note** | Yes | Yes | Yes (Credit Invoice) | Yes (Credit Memo) | Yes | N/A | N/A |
| **監査証跡** | Event API | Activity Log | Webhooks | Transaction Journal | DB records | 操作ログ | 操作ログ |
| **日本対応** | - | - | - | E-Invoice | - | インボイス制度 | インボイス制度 |

*Lago: Regenerateは2026年3月時点でUI操作のみ

---

## 設計上の推奨事項 (自社システム構築時)

### 1. 基本パターンの選択

```
推奨: Void-and-Recreate パターン (Stripe Revision型)

理由:
- 会計上の整合性が保たれる（元請求書はVoidで残る）
- 監査証跡が自然に形成される
- 日本のインボイス制度とも親和性が高い
```

### 2. データモデル設計例

```typescript
interface Invoice {
  id: string;
  number: string;           // INV-2026-0001
  status: 'draft' | 'open' | 'paid' | 'void' | 'uncollectible';

  // 再発行関連
  voidedAt?: Date;
  voidReason?: string;
  originalInvoiceId?: string;   // 再発行元のInvoice ID
  latestRevisionId?: string;    // 最新リビジョンのInvoice ID
  revisionNumber: number;       // 0: original, 1: first revision, ...

  // 日本インボイス制度
  qualifiedInvoiceIssuerNumber?: string;  // T + 13桁
  isModifiedInvoice: boolean;             // 修正インボイスフラグ
  modificationNote?: string;              // 修正内容の説明

  // 監査証跡
  createdBy: string;
  createdAt: Date;
  updatedAt: Date;
  auditLog: AuditEntry[];
}

interface AuditEntry {
  action: 'created' | 'finalized' | 'voided' | 'revised' | 'paid';
  performedBy: string;
  performedAt: Date;
  details: Record<string, any>;
  previousValues?: Record<string, any>;  // 変更前の値
}
```

### 3. 再発行フロー設計

```typescript
async function reissueInvoice(
  originalInvoiceId: string,
  changes: InvoiceChanges,
  reason: string
): Promise<Invoice> {
  // 1. 元のInvoiceをVoid
  const original = await voidInvoice(originalInvoiceId, reason);

  // 2. 新しいInvoiceを作成（元のデータをコピー）
  const newInvoice = await createInvoice({
    ...original.toReissueData(),
    ...changes,
    originalInvoiceId: original.id,
    revisionNumber: original.revisionNumber + 1,
    number: generateRevisionNumber(original.number, original.revisionNumber + 1),
    isModifiedInvoice: true,
    modificationNote: reason,
  });

  // 3. 元のInvoiceにlatestRevisionを設定
  await updateInvoice(original.id, {
    latestRevisionId: newInvoice.id,
  });

  // 4. 監査ログ記録
  await recordAudit({
    invoiceId: original.id,
    action: 'revised',
    details: { newInvoiceId: newInvoice.id, reason, changes },
  });

  return newInvoice;
}
```

---

## 日本インボイス制度のまとめ

### 法的要件
1. **発行義務**: 適格請求書発行事業者は、誤りのあるインボイスの修正版を交付する義務がある
2. **買い手の制限**: 買い手側で適格請求書に追記・修正を行うことは原則不可
3. **保存義務**: 元のインボイスと修正版の両方を保存する必要がある

### 2つの修正方法 (国税庁認定)
| 方法 | 内容 | 適用場面 |
|------|------|----------|
| **全項目再交付** | 修正を加えた上で全ての記載事項を記載した適格請求書を再発行 | 大幅な修正、宛名変更 |
| **修正事項のみ記載** | 元のインボイスとの関連性を明示し、修正した事項のみ記載した書類を交付 | 軽微な修正（金額の一部訂正等） |

### 参考URL
- [修正インボイスとは？](https://media.invoice.ne.jp/column/invoices/correction-invoice.html)
- [国税庁 インボイス制度Q&A](https://www.nta.go.jp/taxes/shiraberu/zeimokubetsu/shohi/keigenzeiritsu/qa_invoice_mokuji.htm)
- [国税庁 インボイス制度の手引き (PDF)](https://www.nta.go.jp/taxes/shiraberu/zeimokubetsu/shohi/keigenzeiritsu/pdf/0022009-090.pdf)
