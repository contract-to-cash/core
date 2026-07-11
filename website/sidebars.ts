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
        'guides/data-protection',
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
      ],
    },
  ],
};

export default sidebars;
