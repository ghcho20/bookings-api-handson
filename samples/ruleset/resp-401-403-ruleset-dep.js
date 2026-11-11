export default function scopedOperationRequires403(operation, _opts, context) {
  if (!operation || typeof operation !== 'object' || Array.isArray(operation)) {
    return [];
  }

  const root = context?.document?.data;
  const security = operation.security ?? root?.security;
  if (!Array.isArray(security) || security.length === 0) {
    return [];
  }

  const hasScopedAlternative = security.some(
    (requirement) =>
      requirement &&
      typeof requirement === 'object' &&
      Object.values(requirement).some(
        (scopes) => Array.isArray(scopes) && scopes.length > 0
      )
  );
  if (!hasScopedAlternative) {
    return [];
  }

  const responses = operation.responses || {};
  if (responses['403']) {
    return [];
  }

  return [
    {
      message:
        'Operation accepts a scoped security alternative (e.g. OAuth2 with scopes) but documents no 403 response for a valid-but-insufficiently-scoped caller (RFC 9110 section 15.5.4).',
    },
  ];
}
