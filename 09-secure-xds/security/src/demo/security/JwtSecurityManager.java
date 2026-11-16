package demo.security;

import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.security.KeyFactory;
import java.security.PublicKey;
import java.security.Signature;
import java.security.spec.X509EncodedKeySpec;
import java.util.ArrayList;
import java.util.Base64;
import java.util.List;
import java.util.Properties;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import org.apache.geode.security.AuthenticationFailedException;
import org.apache.geode.security.ResourcePermission;
import org.apache.geode.security.SecurityManager;
import org.apache.logging.log4j.LogManager;
import org.apache.logging.log4j.Logger;

/**
 * Authenticates GemFire for gRPC callers by a signed JWT, and authorizes them by the GemFire
 * permissions the token lists. Also authenticates the cluster's own users (members joining, gfsh)
 * by username and password, because one SecurityManager guards the whole cluster.
 *
 * <p>A gRPC call's {@code authorization: Bearer <token>} header reaches {@link #authenticate} as
 * the {@code security-token} property ({@link SecurityManager#TOKEN}). The token is an RS256 JWT:
 * <pre>{"sub": "bob", "exp": 1794850000, "perms": ["DATA:READ"]}</pre>
 * {@code perms} are GemFire permission strings (RESOURCE:OPERATION[:REGION[:KEY]]), so a request
 * is allowed when any of them {@link ResourcePermission#implies implies} the one it needs.
 *
 * <p>Tokens are signed by an issuer (an identity provider; here the issuer/ service) with its RSA
 * private key. The cluster holds only the public key (env JWT_PUBLIC_KEY_FILE), so it can check
 * tokens but never make one. Only RS256 is accepted: a token claiming "none" or HS256 is refused,
 * which closes the classic trick of signing a forged token with the public key as an HMAC secret.
 *
 * <p>Settings, from gfsecurity.properties ({@link #init}): security-admin-password,
 * security-cluster-password and security-metrics-password for the three built-in users.
 */
public class JwtSecurityManager implements SecurityManager {

  private static final Logger log = LogManager.getLogger();

  /** Who a call (or a cluster user) is, and what it may do. */
  public record User(String name, List<ResourcePermission> perms) {
    @Override
    public String toString() {
      return name;
    }
  }

  private PublicKey issuerKey;
  private String adminPassword;
  private String clusterPassword;
  private String metricsPassword;

  @Override
  public void init(Properties props) {
    String file = System.getenv("JWT_PUBLIC_KEY_FILE");
    if (file == null || file.isBlank()) throw new IllegalStateException("JWT_PUBLIC_KEY_FILE is not set");
    try {
      // PEM "PUBLIC KEY" = base64 of an X.509 SubjectPublicKeyInfo.
      String pem = Files.readString(Path.of(file)).replaceAll("-----[A-Z ]+-----", "").replaceAll("\\s", "");
      issuerKey = KeyFactory.getInstance("RSA").generatePublic(new X509EncodedKeySpec(Base64.getDecoder().decode(pem)));
    } catch (Exception e) {
      throw new IllegalStateException("Can't read the issuer's public key from " + file, e);
    }
    adminPassword = props.getProperty("security-admin-password");
    clusterPassword = props.getProperty("security-cluster-password");
    metricsPassword = props.getProperty("security-metrics-password");
  }

  @Override
  public Object authenticate(Properties credentials) throws AuthenticationFailedException {
    String token = credentials.getProperty(TOKEN);
    if (token != null) {
      try {
        return verify(token);
      } catch (AuthenticationFailedException e) {
        // The caller only ever sees "Missing or invalid token", and GemFire logs just "authentication
        // failed", so say why here: it's the only place the reason is known.
        log.info("Rejected token: {}", e.getMessage());
        throw e;
      }
    }

    String user = credentials.getProperty(USER_NAME);
    String password = credentials.getProperty(PASSWORD);
    // admin: gfsh and operators, everything. cluster: members joining, cluster management.
    // metrics: Prometheus scraping each member's /metrics endpoint, which needs CLUSTER:READ.
    if ("admin".equals(user) && password != null && password.equals(adminPassword)) {
      return new User("admin", List.of(new ResourcePermission("*", "*")));
    }
    if ("cluster".equals(user) && password != null && password.equals(clusterPassword)) {
      return new User("cluster", List.of(new ResourcePermission("CLUSTER", "*")));
    }
    if ("metrics".equals(user) && password != null && password.equals(metricsPassword)) {
      return new User("metrics", List.of(new ResourcePermission("CLUSTER", "READ")));
    }
    throw new AuthenticationFailedException(
        user != null ? "Wrong username or password for " + user : "No bearer token");
  }

  @Override
  public boolean authorize(Object principal, ResourcePermission wanted) {
    if (!(principal instanceof User user)) return false;
    for (ResourcePermission granted : user.perms()) {
      if (granted.implies(wanted)) return true;
    }
    log.info("Denied {} {} (has {})", user.name(), wanted, user.perms());
    return false;
  }

  // ---------------------------------------------------------------------------------------------

  private static final Pattern SUB = Pattern.compile("\"sub\"\\s*:\\s*\"([^\"]*)\"");
  private static final Pattern EXP = Pattern.compile("\"exp\"\\s*:\\s*(\\d+)");
  private static final Pattern PERMS = Pattern.compile("\"perms\"\\s*:\\s*\\[([^\\]]*)\\]");
  private static final Pattern STRING = Pattern.compile("\"([^\"]*)\"");

  /** Checks an RS256 JWT's signature and expiry, and returns its user and permissions. */
  private User verify(String token) {
    String[] parts = token.split("\\.");
    if (parts.length != 3) throw new AuthenticationFailedException("Malformed token: not a JWT");
    String header = decode(parts[0]);
    if (!header.replace(" ", "").contains("\"alg\":\"RS256\"")) {
      throw new AuthenticationFailedException("Unsupported token: alg must be RS256, got " + header);
    }
    if (!signedByIssuer(parts[0] + "." + parts[1], parts[2])) {
      throw new AuthenticationFailedException("Invalid token: not signed by the issuer");
    }

    // The JSON is small and flat; three regular expressions read what's needed.
    String payload = decode(parts[1]);
    Matcher sub = SUB.matcher(payload);
    Matcher exp = EXP.matcher(payload);
    if (!sub.find() || !exp.find()) throw new AuthenticationFailedException("Invalid token: needs sub and exp");
    if (Long.parseLong(exp.group(1)) <= System.currentTimeMillis() / 1000) {
      throw new AuthenticationFailedException("Expired token for " + sub.group(1));
    }
    List<ResourcePermission> perms = new ArrayList<>();
    Matcher list = PERMS.matcher(payload);
    if (list.find()) {
      Matcher s = STRING.matcher(list.group(1));
      while (s.find()) perms.add(permission(s.group(1)));
    }
    return new User(sub.group(1), List.copyOf(perms));
  }

  /** "DATA:READ:orders" → new ResourcePermission("DATA", "READ", "orders"). */
  private static ResourcePermission permission(String text) {
    String[] p = text.split(":");
    return switch (p.length) {
      case 2 -> new ResourcePermission(p[0], p[1]);
      case 3 -> new ResourcePermission(p[0], p[1], p[2]);
      case 4 -> new ResourcePermission(p[0], p[1], p[2], p[3]);
      default -> throw new AuthenticationFailedException("Invalid token: bad permission " + text);
    };
  }

  private boolean signedByIssuer(String signedPart, String signature) {
    try {
      Signature rsa = Signature.getInstance("SHA256withRSA");
      rsa.initVerify(issuerKey);
      rsa.update(signedPart.getBytes(StandardCharsets.US_ASCII));
      return rsa.verify(Base64.getUrlDecoder().decode(signature));
    } catch (Exception e) {
      return false;   // malformed signature bytes count as a bad signature
    }
  }

  private static String decode(String part) {
    try {
      return new String(Base64.getUrlDecoder().decode(part), StandardCharsets.UTF_8);
    } catch (IllegalArgumentException e) {
      throw new AuthenticationFailedException("Malformed token: bad base64");
    }
  }
}
