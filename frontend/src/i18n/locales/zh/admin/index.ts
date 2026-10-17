import overview from './overview'
import channels from './channels'
import accounts from './accounts'
import resources from './resources'
import ops from './ops'
import settings from './settings'
import audit from './audit'
import promptAudit from './promptAudit'
import plugins from './plugins'
import upstreamBalances from './upstreamBalances'
import requestAudit from './requestAudit'
import videoTasks from './videoTasks'
import videoTaskReviews from './videoTaskReviews'

export default {
  ...overview,
  ...channels,
  ...accounts,
  ...resources,
  ...ops,
  ...settings,
  ...audit,
  ...promptAudit,
  ...plugins,
  ...upstreamBalances,
  ...requestAudit,
  ...videoTasks,
  ...videoTaskReviews,
}
