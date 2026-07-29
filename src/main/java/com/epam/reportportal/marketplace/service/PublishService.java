package com.epam.reportportal.marketplace.service;

import com.epam.reportportal.marketplace.domain.AssetsJson;
import com.epam.reportportal.marketplace.domain.MarketplaceManifest;
import com.epam.reportportal.marketplace.domain.PluginJson;
import com.epam.reportportal.marketplace.storage.ObjectStore;
import com.epam.reportportal.marketplace.storage.OptimisticConcurrency;
import com.epam.reportportal.marketplace.util.JsonStore;
import com.epam.reportportal.marketplace.util.Sha256Util;
import com.epam.reportportal.marketplace.util.StoragePaths;
import com.epam.reportportal.marketplace.web.dto.PublishBundle;
import com.epam.reportportal.marketplace.web.dto.PublishResponseDto;
import com.epam.reportportal.marketplace.web.error.ConflictException;
import com.epam.reportportal.marketplace.web.error.NotFoundException;
import com.epam.reportportal.marketplace.web.error.ValidationException;
import com.epam.reportportal.marketplace.web.dto.ValidationFieldError;
import tools.jackson.databind.ObjectMapper;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.List;
import org.springframework.stereotype.Service;

@Service
public class PublishService {

  private final ObjectStore objectStore;
  private final ObjectMapper objectMapper;
  private final ManifestExtractor manifestExtractor;
  private final IndexService indexService;
  private final CdnInvalidationService cdnInvalidationService;

  public PublishService(
      ObjectStore objectStore,
      ObjectMapper objectMapper,
      ManifestExtractor manifestExtractor,
      IndexService indexService,
      CdnInvalidationService cdnInvalidationService) {
    this.objectStore = objectStore;
    this.objectMapper = objectMapper;
    this.manifestExtractor = manifestExtractor;
    this.indexService = indexService;
    this.cdnInvalidationService = cdnInvalidationService;
  }

  public PublishResponseDto publishFirst(PublishBundle bundle) {
    MarketplaceManifest manifest = manifestExtractor.extract(bundle.jar());
    String pluginId = manifest.id();
    String pluginPath = StoragePaths.pluginJson(pluginId);
    if (objectStore.exists(pluginPath)) {
      PluginJson existing = JsonStore.read(objectStore, objectMapper, pluginPath, PluginJson.class);
      if (existing != null && !existing.isRemoved()) {
        throw new ConflictException("Plugin already exists: " + pluginId);
      }
    }
    return publishVersion(pluginId, bundle, manifest, true);
  }

  public PublishResponseDto publishVersion(String pluginId, PublishBundle bundle) {
    MarketplaceManifest manifest = manifestExtractor.extract(bundle.jar());
    if (!pluginId.equals(manifest.id())) {
      throw new ValidationException(
          "Manifest id mismatch",
          List.of(new ValidationFieldError("manifest.id", "Manifest id does not match URL pluginId")));
    }
    String pluginPath = StoragePaths.pluginJson(pluginId);
    if (!objectStore.exists(pluginPath)) {
      throw new NotFoundException("Plugin not found: " + pluginId);
    }
    PluginJson plugin = JsonStore.read(objectStore, objectMapper, pluginPath, PluginJson.class);
    if (plugin == null || plugin.isRemoved()) {
      throw new NotFoundException("Plugin not found: " + pluginId);
    }
    return publishVersion(pluginId, bundle, manifest, false);
  }

  private PublishResponseDto publishVersion(
      String pluginId, PublishBundle bundle, MarketplaceManifest manifest, boolean firstPublish) {
    String version = manifest.version();
    String jarPath = StoragePaths.jarPath(pluginId, version);
    if (objectStore.exists(jarPath)) {
      throw new ConflictException("Version already exists: " + version);
    }

    String sha256 = Sha256Util.hash(bundle.jar());
    Instant publishedAt = Instant.now();

    objectStore.writeBytes(jarPath, bundle.jar());
    objectStore.writeBytes(StoragePaths.manifestPath(pluginId, version),
        serialize(manifest));

    boolean hasChangelog = bundle.changelog() != null && bundle.changelog().length > 0;
    if (hasChangelog) {
      objectStore.writeBytes(StoragePaths.changelogPath(pluginId, version), bundle.changelog());
    }

    List<String> screenshotNames = new ArrayList<>();
    if (bundle.screenshots() != null) {
      if (bundle.screenshots().size() > 5) {
        throw new ValidationException("Too many screenshots", List.of(
            new ValidationFieldError("screenshots", "Maximum 5 screenshots allowed")));
      }
      for (PublishBundle.ScreenshotPart part : bundle.screenshots()) {
        if (part.bytes() != null && part.bytes().length > 2 * 1024 * 1024) {
          throw new ValidationException("Screenshot too large", List.of(
              new ValidationFieldError("screenshots", "Each screenshot must be <= 2MB")));
        }
        String filename = part.filename() != null && !part.filename().isBlank()
            ? part.filename()
            : "screenshot.png";
        String lower = filename.toLowerCase();
        if (!(lower.endsWith(".png") || lower.endsWith(".jpg") || lower.endsWith(".jpeg"))) {
          throw new ValidationException("Invalid screenshot type", List.of(
              new ValidationFieldError("screenshots", "Only PNG or JPEG allowed")));
        }
        screenshotNames.add(filename);
        objectStore.writeBytes(StoragePaths.screenshotPath(pluginId, version, filename), part.bytes());
      }
    }
    screenshotNames.sort(String::compareTo);

    AssetsJson assets = new AssetsJson();
    assets.setHasChangelog(hasChangelog);
    assets.setScreenshots(screenshotNames);
    assets.setSha256(sha256);
    assets.setPublishedAt(publishedAt);
    objectStore.writeBytes(StoragePaths.assetsPath(pluginId, version), serialize(assets));

    OptimisticConcurrency.execute(() -> updatePluginJson(pluginId, version, firstPublish));

    OptimisticConcurrency.execute(() -> indexService.regenerateIndex());

    cdnInvalidationService.invalidatePaths(List.of(
        "/" + StoragePaths.INDEX,
        "/plugins/" + pluginId + "/plugin.json",
        "/plugins/" + pluginId + "/versions/" + version + "/*"));

    return new PublishResponseDto(pluginId, version, sha256);
  }

  private void updatePluginJson(String pluginId, String version, boolean firstPublish) {
    String pluginPath = StoragePaths.pluginJson(pluginId);
    ObjectStore.StoredObject stored = objectStore.exists(pluginPath)
        ? objectStore.read(pluginPath)
        : new ObjectStore.StoredObject(new byte[0], -1);

    PluginJson plugin;
    if (stored.data().length == 0) {
      plugin = new PluginJson();
      plugin.setId(pluginId);
    } else {
      try {
        plugin = objectMapper.readValue(stored.data(), PluginJson.class);
      } catch (Exception e) {
        throw new IllegalStateException("Invalid plugin.json", e);
      }
    }
    if (!firstPublish && !pluginId.equals(plugin.getId())) {
      throw new ValidationException("Plugin id mismatch", List.of(
          new ValidationFieldError("manifest.id", "Manifest id does not match existing plugin")));
    }
    plugin.setId(pluginId);
    plugin.setTier(com.epam.reportportal.marketplace.domain.TrustTier.OFFICIAL);
    if (!plugin.getVersions().contains(version)) {
      plugin.getVersions().add(version);
      plugin.getVersions().sort(Comparator.reverseOrder());
    }
    plugin.setLatestVersion(plugin.getVersions().get(0));

    JsonStore.writeIfGenerationMatch(objectStore, objectMapper, pluginPath, plugin, stored.generation());
  }

  private byte[] serialize(Object value) {
    try {
      return objectMapper.writerWithDefaultPrettyPrinter().writeValueAsBytes(value);
    } catch (Exception e) {
      throw new IllegalStateException(e);
    }
  }
}
