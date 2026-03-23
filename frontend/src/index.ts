import './styles/tailwind.css';
import type { TangraModule } from './sdk';
import routes from './routes';
import { useSigningTemplateStore } from './stores/signing-template.state';
import { useSigningSubmissionStore } from './stores/signing-submission.state';
import { useSigningCertificateStore } from './stores/signing-certificate.state';
import enUS from './locales/en-US.json';

const signingModule: TangraModule = {
  id: 'signing',
  version: '1.0.0',
  routes,
  stores: {
    'signing-template': useSigningTemplateStore,
    'signing-submission': useSigningSubmissionStore,
    'signing-certificate': useSigningCertificateStore,
  },
  locales: {
    'en-US': enUS,
  },
};

export default signingModule;
