import type {ReactNode} from 'react';
import clsx from 'clsx';
import Heading from '@theme/Heading';
import Translate from '@docusaurus/Translate';
import styles from './styles.module.css';

type FeatureItem = {
  titleId: string;
  titleDefault: string;
  descriptionId: string;
  descriptionDefault: string;
  icon: string;
};

const FeatureList: FeatureItem[] = [
  {
    titleId: 'homepage.features.eventSourcing.title',
    titleDefault: 'Event Sourcing',
    icon: '📋',
    descriptionId: 'homepage.features.eventSourcing.description',
    descriptionDefault:
      'Complete audit trail for every operation. Reconstruct state at any point in time. Built-in snapshot support for fast recovery.',
  },
  {
    titleId: 'homepage.features.pluginArchitecture.title',
    titleDefault: 'Plugin Architecture',
    icon: '🔌',
    descriptionId: 'homepage.features.pluginArchitecture.description',
    descriptionDefault:
      'Extend billing logic with discount hooks, tax calculators, lifecycle observers, and invoice generators. ISP-compliant — implement only what you need.',
  },
  {
    titleId: 'homepage.features.billingModels.title',
    titleDefault: 'Multiple Billing Models',
    icon: '💰',
    descriptionId: 'homepage.features.billingModels.description',
    descriptionDefault:
      'One-time purchases, subscriptions, and usage-based billing out of the box. Supports tiered pricing, volume discounts, and per-contract overrides.',
  },
  {
    titleId: 'homepage.features.ddd.title',
    titleDefault: 'Domain-Driven Design',
    icon: '🏗️',
    descriptionId: 'homepage.features.ddd.description',
    descriptionDefault:
      'Clean domain model with Contract, Invoice, Payment, and Usage aggregates. Clear boundaries, value objects, and repository interfaces.',
  },
  {
    titleId: 'homepage.features.paymentGateway.title',
    titleDefault: 'Payment Gateway Abstraction',
    icon: '🔄',
    descriptionId: 'homepage.features.paymentGateway.description',
    descriptionDefault:
      'Pluggable payment gateway interface supporting charge, authorize/capture, refund, and payment method management. Stripe-style hierarchical fallback resolution.',
  },
  {
    titleId: 'homepage.features.productionReady.title',
    titleDefault: 'Production Ready Patterns',
    icon: '🚀',
    descriptionId: 'homepage.features.productionReady.description',
    descriptionDefault:
      'Contract renewal with price promotion, payment-gated provisioning, credit ledger with FIFO consumption, and batch processing for large-scale operations.',
  },
];

function Feature({titleId, titleDefault, descriptionId, descriptionDefault, icon}: FeatureItem) {
  return (
    <div className={clsx('col col--4')}>
      <div className="text--center padding-horiz--md" style={{marginBottom: '2rem'}}>
        <div style={{fontSize: '3rem', marginBottom: '1rem'}}>{icon}</div>
        <Heading as="h3">
          <Translate id={titleId}>{titleDefault}</Translate>
        </Heading>
        <p>
          <Translate id={descriptionId}>{descriptionDefault}</Translate>
        </p>
      </div>
    </div>
  );
}

export default function HomepageFeatures(): ReactNode {
  return (
    <section className={styles.features}>
      <div className="container">
        <div className="row">
          {FeatureList.map((props, idx) => (
            <Feature key={idx} {...props} />
          ))}
        </div>
      </div>
    </section>
  );
}
