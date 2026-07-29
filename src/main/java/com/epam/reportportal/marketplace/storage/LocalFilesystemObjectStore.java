package com.epam.reportportal.marketplace.storage;

import com.epam.reportportal.marketplace.config.MarketplaceProperties;
import tools.jackson.databind.ObjectMapper;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.nio.file.StandardOpenOption;
import java.security.GeneralSecurityException;
import java.security.MessageDigest;
import java.security.SecureRandom;
import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Base64;
import java.util.Comparator;
import java.util.List;
import java.util.Optional;
import java.util.stream.Stream;
import javax.crypto.Mac;
import javax.crypto.spec.SecretKeySpec;

public class LocalFilesystemObjectStore implements ObjectStore {

  private final Path root;
  private final MarketplaceProperties properties;
  private final byte[] signedUrlKey = new byte[32];

  public LocalFilesystemObjectStore(MarketplaceProperties properties, ObjectMapper objectMapper) {
    this.properties = properties;
    this.root = Path.of(properties.getStorage().getLocal().getRoot()).toAbsolutePath().normalize();
    new SecureRandom().nextBytes(signedUrlKey);
    try {
      Files.createDirectories(root);
    } catch (IOException e) {
      throw new ObjectStoreException("Failed to create storage root: " + root, e);
    }
  }

  @Override
  public byte[] readBytes(String key) {
    return read(key).data();
  }

  @Override
  public StoredObject read(String key) {
    Path file = resolve(key);
    if (!Files.exists(file)) {
      throw new ObjectStoreException("Object not found: " + key);
    }
    try {
      return new StoredObject(Files.readAllBytes(file), readGeneration(file));
    } catch (IOException e) {
      throw new ObjectStoreException("Failed to read: " + key, e);
    }
  }

  @Override
  public void writeBytes(String key, byte[] data) {
    writeBytesIfGenerationMatch(key, data, -1);
  }

  @Override
  public void writeBytesIfGenerationMatch(String key, byte[] data, long expectedGeneration) {
    Path file = resolve(key);
    try {
      Files.createDirectories(file.getParent());
      if (expectedGeneration >= 0 && Files.exists(file)) {
        long current = readGeneration(file);
        if (current != expectedGeneration) {
          throw new ConcurrentModificationException(
              "Generation mismatch for " + key + ": expected " + expectedGeneration + " but was " + current);
        }
      }
      Path temp = file.resolveSibling(file.getFileName() + ".tmp");
      Files.write(temp, data, StandardOpenOption.CREATE, StandardOpenOption.TRUNCATE_EXISTING);
      Files.move(temp, file, StandardCopyOption.REPLACE_EXISTING, StandardCopyOption.ATOMIC_MOVE);
      writeGeneration(file, Instant.now().toEpochMilli());
    } catch (ConcurrentModificationException e) {
      throw e;
    } catch (IOException e) {
      throw new ObjectStoreException("Failed to write: " + key, e);
    }
  }

  @Override
  public void delete(String key) {
    Path file = resolve(key);
    try {
      Files.deleteIfExists(file);
      Files.deleteIfExists(metaPath(file));
    } catch (IOException e) {
      throw new ObjectStoreException("Failed to delete: " + key, e);
    }
  }

  @Override
  public List<String> listPrefix(String prefix) {
    Path dir = resolve(prefix);
    if (!Files.exists(dir)) {
      return List.of();
    }
    List<String> keys = new ArrayList<>();
    try (Stream<Path> walk = Files.walk(dir)) {
      walk.filter(Files::isRegularFile)
          .filter(p -> !p.getFileName().toString().endsWith(".gen"))
          .forEach(p -> keys.add(toKey(p)));
    } catch (IOException e) {
      throw new ObjectStoreException("Failed to list prefix: " + prefix, e);
    }
    keys.sort(Comparator.naturalOrder());
    return keys;
  }

  @Override
  public boolean exists(String key) {
    return Files.exists(resolve(key));
  }

  @Override
  public Optional<Long> getGeneration(String key) {
    Path file = resolve(key);
    if (!Files.exists(file)) {
      return Optional.empty();
    }
    return Optional.of(readGeneration(file));
  }

  @Override
  public SignedUrl createSignedUrl(String key, Duration maxTtl) {
    Duration ttl = maxTtl.compareTo(Duration.ofSeconds(60)) > 0 ? Duration.ofSeconds(60) : maxTtl;
    Instant expiresAt = Instant.now().plus(ttl);
    String baseUrl = properties.getCdn().getBaseUrl();
    if (baseUrl != null && !baseUrl.isBlank()) {
      String normalizedBase = baseUrl.replaceAll("/+$", "");
      String signedBase = normalizedBase.endsWith("/cdn")
          ? normalizedBase.substring(0, normalizedBase.length() - 4) + "/cdn-private"
          : normalizedBase + "/cdn-private";
      long expires = expiresAt.getEpochSecond();
      String signature = sign(key, expires);
      String url = signedBase + "/" + key + "?expires=" + expires + "&signature=" + signature;
      return new SignedUrl(url, expiresAt);
    }
    throw new ObjectStoreException("CDN base URL is required for local signed URLs");
  }

  @Override
  public boolean verifySignedUrl(String key, long expiresAtEpochSecond, String signature) {
    long now = Instant.now().getEpochSecond();
    if (expiresAtEpochSecond < now || expiresAtEpochSecond > now + 65 || signature == null) {
      return false;
    }
    byte[] expected = sign(key, expiresAtEpochSecond).getBytes(StandardCharsets.US_ASCII);
    byte[] actual = signature.getBytes(StandardCharsets.US_ASCII);
    return MessageDigest.isEqual(expected, actual);
  }

  private String sign(String key, long expiresAtEpochSecond) {
    try {
      Mac mac = Mac.getInstance("HmacSHA256");
      mac.init(new SecretKeySpec(signedUrlKey, "HmacSHA256"));
      byte[] payload = (key + "\n" + expiresAtEpochSecond).getBytes(StandardCharsets.UTF_8);
      return Base64.getUrlEncoder().withoutPadding().encodeToString(mac.doFinal(payload));
    } catch (GeneralSecurityException e) {
      throw new ObjectStoreException("Failed to sign local artifact URL", e);
    }
  }

  private Path resolve(String key) {
    String normalized = key.replace('\\', '/').replaceAll("^/+", "");
    Path resolved = root.resolve(normalized).normalize();
    if (!resolved.startsWith(root)) {
      throw new ObjectStoreException("Invalid key path: " + key);
    }
    return resolved;
  }

  private String toKey(Path file) {
    return root.relativize(file).toString().replace('\\', '/');
  }

  private Path metaPath(Path file) {
    return file.resolveSibling(file.getFileName() + ".gen");
  }

  private long readGeneration(Path file) {
    Path meta = metaPath(file);
    if (Files.exists(meta)) {
      try {
        String value = Files.readString(meta).trim();
        return Long.parseLong(value);
      } catch (IOException | NumberFormatException ignored) {
        // fall through
      }
    }
    try {
      return Files.getLastModifiedTime(file).toMillis();
    } catch (IOException e) {
      return 0L;
    }
  }

  private void writeGeneration(Path file, long generation) throws IOException {
    Files.writeString(metaPath(file), Long.toString(generation));
  }
}
