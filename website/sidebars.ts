import type {SidebarsConfig} from '@docusaurus/plugin-content-docs';

const sidebars: SidebarsConfig = {
  docsSidebar: [
    'introduction',
    'quick-start',
    'architecture',
    {
      type: 'category',
      label: 'Core Concepts',
      items: [
        'concepts/domain-model',
        'concepts/event-sourcing',
        'concepts/plugin-system',
        'concepts/payment-gateway',
      ],
    },
    {
      type: 'category',
      label: 'Guides',
      items: [
        'guides/integration',
        'guides/custom-plugin',
        'guides/temporal-queries',
        'guides/payment-integration',
        'guides/postgres-payment-repository',
        'guides/performance',
      ],
    },
    {
      type: 'category',
      label: 'Examples',
      items: [
        'examples/billing-flow',
        'examples/event-sourcing-demo',
        'examples/plugin-pipeline',
        'examples/lifecycle',
        'examples/pricing-models',
      ],
    },
  ],
  apiSidebar: [
    {
      type: 'category',
      label: 'API Reference',
      items: [
        'api/domain-types',
        'api/services',
        'api/plugin-hooks',
        'api/event-store',
      ],
    },
    {
      type: 'category',
      label: 'Internals',
      items: [
        'internals/domain-model',
        'internals/event-sourcing',
        'internals/plugin-system',
        'internals/payment-gateway',
        'internals/metrics-invoicegen',
        'internals/codebase-review-20260327',
        'decisions/design-decisions',
      ],
    },
    {
      type: 'category',
      label: 'Research',
      items: [
        'research/2026-04-10-payment-idempotency-patterns',
        'research/2026-03-28-billing-interval-industry-standards',
        'research/2026-03-28-invoice-reissue-industry-standards',
        'research/2026-01-30-design-review-improvements',
      ],
    },
  ],
};

export default sidebars;
