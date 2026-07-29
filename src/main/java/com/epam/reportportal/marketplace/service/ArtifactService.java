package com.epam.reportportal.marketplace.service;

import com.epam.reportportal.marketplace.config.MarketplaceProperties;
import com.epam.reportportal.marketplace.domain.AccessTier;
import com.epam.reportportal.marketplace.domain.BlockedVersion;
import com.epam.reportportal.marketplace.domain.MarketplaceManifest;
import com.epam.reportportal.marketplace.domain.PluginJson;
import com.epam.reportportal.marketplace.storage.ObjectStore;
import com.epam.reportportal.marketplace.util.JsonStore;
import com.epam.reportportal.marketplace.util.StoragePaths;
import com.epam.reportportal.marketplace.web.dto.ArtifactResult;
import com.epam.reportportal.marketplace.web.dto.BlockedArtifactErrorDto;
import com.epam.reportportal.marketplace.web.dto.PluginTombstoneDto;
import com.epam.reportportal.marketplace.web.dto.PremiumArtifactResponseDto;
import com.epam.reportportal.marketplace.web.error.ForbiddenException;
import com.epam.reportportal.marketplace.web.error.GoneException;
import com.epam.reportportal.marketplace.web.error.NotFoundException;
import com.epam.reportportal.marketplace.web.error.UnauthorizedException;
import com.fasterxml.jackson.databind.ObjectMapper;
import java.time.Duration;
import org.springframework.stereotype.Service;

@Service
public class ArtifactService {

  private final ObjectStore objectStore;
  private final ObjectMapper objectMapper;
  private final MarketplaceProperties properties;
  private final LicenseService licenseService;

  public ArtifactService(
      ObjectStore objectStore,
      ObjectMapper objectMapper,
      MarketplaceProperties properties,
      LicenseService licenseService) {
    this.objectStore = objectStore;
    this.objectMapper = objectMapper;
    this.properties = properties;
    this.licenseService = licenseService;
  }

  public ArtifactResult resolveArtifact(String pluginId, String version, String licenseJwt) {
    PluginJson plugin = loadPlugin(pluginId);
    if (plugin.isRemoved()) {
      throw new GoneException(new PluginTombstoneDto(
          plugin.getRemoved(), plugin.getRemovalReason(), plugin.getRemovedBy()));
    }
    if (!plugin.getVersions().contains(version)) {
      throw new NotFoundException("Version not found: " + version);
    }
    BlockedVersion blocked = plugin.getBlockedVersions().stream()
        .filter(b -> version.equals(b.version()))
        .findFirst()
        .orElse(null);
    if (blocked != null) {
      throw new ForbiddenException(new BlockedArtifactErrorDto(true, blocked.blockedAt(), blocked.reason()));
    }

    MarketplaceManifest manifest = JsonStore.read(
        objectStore, objectMapper, StoragePaths.manifestPath(pluginId, version), MarketplaceManifest.class);
    AccessTier access = manifest != null && manifest.access() != null ? manifest.access() : AccessTier.PUBLIC;

    String jarPath = StoragePaths.jarPath(pluginId, version);
    if (!objectStore.exists(jarPath)) {
      throw new NotFoundException("Artifact not found");
    }

    if (access == AccessTier.PREMIUM) {
      if (licenseJwt == null || licenseJwt.isBlank()) {
        throw new UnauthorizedException("Premium artifact requires license JWT");
      }
      licenseService.verifyLicenseJwt(licenseJwt, pluginId);
      ObjectStore.SignedUrl signed = objectStore.createSignedUrl(jarPath, Duration.ofSeconds(60));
      return ArtifactResult.premium(new PremiumArtifactResponseDto(signed.url(), signed.expiresAt()));
    }

    String base = properties.getCdn().getBaseUrl().replaceAll("/$", "");
    return ArtifactResult.redirect(base + "/" + jarPath);
  }

  private PluginJson loadPlugin(String pluginId) {
    String path = StoragePaths.pluginJson(pluginId);
    if (!objectStore.exists(path)) {
      throw new NotFoundException("Plugin not found: " + pluginId);
    }
    PluginJson plugin = JsonStore.read(objectStore, objectMapper, path, PluginJson.class);
    if (plugin == null) {
      throw new NotFoundException("Plugin not found: " + pluginId);
    }
    return plugin;
  }
}
