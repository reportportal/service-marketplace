package com.epam.reportportal.marketplace.service;

import com.epam.reportportal.marketplace.domain.AccessTier;
import com.epam.reportportal.marketplace.domain.MarketplaceManifest;
import com.epam.reportportal.marketplace.domain.PluginCategory;
import com.epam.reportportal.marketplace.web.dto.ValidationFieldError;
import com.epam.reportportal.marketplace.web.error.ValidationException;
import tools.jackson.databind.ObjectMapper;
import java.io.ByteArrayInputStream;
import java.io.IOException;
import java.util.ArrayList;
import java.util.List;
import java.util.zip.ZipEntry;
import java.util.zip.ZipInputStream;
import org.springframework.stereotype.Service;

@Service
public class ManifestExtractor {

  public static final String MANIFEST_ENTRY = "marketplace-manifest.json";

  private final ObjectMapper objectMapper;

  public ManifestExtractor(ObjectMapper objectMapper) {
    this.objectMapper = objectMapper;
  }

  public MarketplaceManifest extract(byte[] jarBytes) {
    byte[] manifestBytes = readManifestBytes(jarBytes);
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

  private byte[] readManifestBytes(byte[] jarBytes) {
    try (ZipInputStream zis = new ZipInputStream(new ByteArrayInputStream(jarBytes))) {
      ZipEntry entry;
      while ((entry = zis.getNextEntry()) != null) {
        if (MANIFEST_ENTRY.equals(entry.getName())) {
          return zis.readAllBytes();
        }
      }
    } catch (IOException e) {
      throw new ValidationException("Failed to read jar", List.of(
          new ValidationFieldError("jar", "Unable to read jar archive")));
    }
    throw new ValidationException("Missing manifest", List.of(
        new ValidationFieldError("manifest", "marketplace-manifest.json not found in jar")));
  }

  public void validate(MarketplaceManifest manifest) {
    List<ValidationFieldError> errors = new ArrayList<>();
    require(errors, "manifest.id", manifest.id());
    require(errors, "manifest.name", manifest.name());
    require(errors, "manifest.version", manifest.version());
    require(errors, "manifest.description", manifest.description());
    require(errors, "manifest.license", manifest.license());
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
