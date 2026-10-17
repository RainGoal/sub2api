export default {
  videoTasks: {
    connection: 'Video task connection',
    bblabuPreset: 'Apply BBLabu connection preset',
    supplierName: 'Supplier name',
    explicitBaseUrl: 'Enter the Base URL and API key below. Custom suppliers use the same protocol; connection presets do not authorize models.',
    baseUrlRequired: 'Video Tasks JSON v1 requires an explicit Base URL.',
    discover: 'Discover upstream models',
    discovering: 'Discovering…',
    discoveryHint: 'Review discovery results, then select models and configure purchase and sales prices.',
    saveBeforeDiscovery: 'The connection URL changed. Enter a new key to preview, or save before syncing with the existing key.',
    discoveredCount: '{count} models discovered; none authorized automatically',
    noDiscoveredModels: 'The upstream returned no models.',
    discoveryFailed: 'Model discovery failed. Check the connection, key, and permissions.',
    enabledModels: 'Authorized video models',
    search: 'Search exact model IDs',
    loadingTemplates: 'Loading server capability templates…',
    templatesFailed: 'Could not load capability templates. Retry or check administrator access.',
    retry: 'Reload',
    noMatchingModels: 'No matching capability template. Configure a custom model below.',
    chooseModels: 'Explicitly select at least one video model with configured capabilities.',
    capabilityJSON: 'Custom model capability JSON',
    capabilityHint: 'Edit an array of capability objects, or select a template first. The backend validates the full schema. Unverified combinations remain disabled. New models require separate authorization.',
    invalidJSON: 'Capabilities must be a valid JSON array of objects, each containing model.',
    fixedSpecification: 'Fixed specification',
    perVideo: 'Per video',
    perSecond: 'Per second'
  }
}
