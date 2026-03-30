import type {SidebarsConfig} from '@docusaurus/plugin-content-docs';

const sidebars: SidebarsConfig = {
  docsSidebar: [
    'introduction',
    'quick-start',
    {
      type: 'category',
      label: 'Guides',
      items: [
        'guides/architecture',
        'guides/domain-model',
        'guides/billing-and-pricing',
        'guides/event-sourcing',
        'guides/plugin-system',
        'guides/payment-integration',
        'guides/system-integration',
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
  ],
};

export default sidebars;
