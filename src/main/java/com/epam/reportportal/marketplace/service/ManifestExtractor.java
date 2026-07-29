package com.epam.reportportal.marketplace.service;

import com.epam.reportportal.marketplace.domain.AccessTier;
import com.epam.reportportal.marketplace.domain.MarketplaceManifest;
import com.epam.reportportal.marketplace.domain.PluginCategory;
import com.epam.reportportal.marketplace.util.PluginIdentifiers;
import com.epam.reportportal.marketplace.web.dto.ValidationFieldError;
import com.epam.reportportal.marketplace.web.error.ValidationException;
import tools.jackson.databind.ObjectMapper;
import java.util.ArrayList;
import java.util.List;
import org.springframework.stereotype.Service;

@Service
public class ManifestExtractor {

  public static final String MANIFEST_ENTRY = "marketplace-manifest.json";

  private final ObjectMapper objectMapper;
  private final ManifestArchiveReader archiveReader = new ManifestArchiveReader();

  public ManifestExtractor(ObjectMapper objectMapper) {
    this.objectMapper = objectMapper;
  }

  public MarketplaceManifest extract(byte[] jarBytes) {
    byte[] manifestBytes = archiveReader.read(jarBytes, MANIFEST_ENTRY);
    MarketplaceManifest manifest;
    try {
      manifest = objectMapper.readValue(manifestBytes, MarketplaceManifest.class);
    } catch (RuntimeException e) {
      throw new ValidationException("Invalid marketplace-manifest.json", List.of(
          new ValidationFieldError("manifest", "Failed to parse marketplace-manifest.json")));
    }
    validate(manifest);
    return manifest;
  }

  public void validate(MarketplaceManifest manifest) {
    List<ValidationFieldError> errors = new ArrayList<>();
    require(errors, "manifest.id", manifest.id());
    require(errors, "manifest.name", manifest.name());
    require(errors, "manifest.version", manifest.version());
    require(errors, "manifest.description", manifest.description());
    require(errors, "manifest.license", manifest.license());
    if (!isBlank(manifest.id()) && !PluginIdentifiers.isValidId(manifest.id())) {
      errors.add(new ValidationFieldError("manifest.id", PluginIdentifiers.idRequirement()));
    }
    if (!isBlank(manifest.version()) && !PluginIdentifiers.isValidVersion(manifest.version())) {
      errors.add(new ValidationFieldError("manifest.version", PluginIdentifiers.versionRequirement()));
    }
    if (manifest.author() == null || isBlank(manifest.author().name())) {
      errors.add(new ValidationFieldError("manifest.author.name", "Author name is required"));
    }
    if (manifest.compatibility() == null || isBlank(manifest.compatibility().reportportal())) {
      errors.add(new ValidationFieldError("manifest.compatibility.reportportal", "Compatibility is required"));
    }
    if (manifest.category() == null) {
      errors.add(new ValidationFieldError("manifest.category", "Category is required"));
    } else {
      try {
        PluginCategory.fromValue(manifest.category().value());
      } catch (IllegalArgumentException e) {
        errors.add(new ValidationFieldError("manifest.category", e.getMessage()));
      }
    }
    AccessTier access = manifest.access() != null ? manifest.access() : AccessTier.PUBLIC;
    if (access == AccessTier.PREMIUM && isBlank(manifest.contactUrl())) {
      errors.add(new ValidationFieldError("manifest.contactUrl", "contactUrl is required for premium plugins"));
    }
    if (!errors.isEmpty()) {
      throw new ValidationException("Manifest validation failed", errors);
    }
  }

  private void require(List<ValidationFieldError> errors, String field, String value) {
    if (isBlank(value)) {
      errors.add(new ValidationFieldError(field, field + " is required"));
    }
  }

  private boolean isBlank(String value) {
    return value == null || value.isBlank();
  }
}
